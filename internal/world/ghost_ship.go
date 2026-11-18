package world

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

type ghostPhase uint8

const (
	ghostWaiting ghostPhase = iota
	ghostWarmup
	ghostRace
	ghostBoss
	ghostFinished
)

type ghostRoster struct {
	partyID int64
	leader  int64
	members [content.GhostTeamSize]int64
}

func (p ghostRoster) contains(id int64) bool {
	if id <= 0 {
		return false
	}
	for _, candidate := range p.members {
		if candidate == id {
			return true
		}
	}
	return false
}

type ghostMatch struct {
	mu        sync.Mutex
	room      content.GhostRoom
	phase     ghostPhase
	deadline  atomic.Int64
	minimum   atomic.Int32
	team      [content.GhostTeams]ghostRoster
	challenge ghostRoster
	stage     [content.GhostTeams]int
	damage    [content.GhostTeams]int64
	total     map[int64]int64
	points    [content.GhostTeams]int64
	moving    bool
}

func (g *ghostMatch) teamOf(id int64) (int, bool) {
	for i, team := range g.team {
		if team.contains(id) {
			return i, true
		}
	}
	return 0, false
}

var ghostTeamNames = [...]string{"redTeam", "blueTeam"}

func (s *Server) ghostRosterAt(channel int, party domain.PartySnapshot, mapID int) (ghostRoster, []eventOccupant, string) {
	if party.ID == 0 || len(party.Members) != content.GhostTeamSize {
		return ghostRoster{}, nil, "party_size"
	}
	roster := ghostRoster{partyID: party.ID, leader: party.LeaderID}
	group := []eventOccupant{}
	for _, peer := range s.eventOccupants(channel, []int{mapID}) {
		p := partySession(peer.live)
		if _, ok := party.Member(p.CharacterID); !ok {
			continue
		}
		if p.Character.HP <= 0 || s.tradeActive(peer.live) {
			return ghostRoster{}, nil, "party_size"
		}
		if p.Character.Level < content.GhostLevelMin || p.Character.Level > content.GhostLevelMax {
			return ghostRoster{}, nil, "level"
		}
		roster.members[len(group)] = p.CharacterID
		group = append(group, peer)
	}
	if len(group) != content.GhostTeamSize {
		return ghostRoster{}, nil, "party_size"
	}
	return roster, group, ""
}

func (s *Server) ghostQueue(ctx context.Context, actor domain.CharacterSession, room content.GhostRoom) (string, error) {
	if !room.Valid() || actor.Character.MapID != content.GhostLobby || s.events == nil || s.parties == nil || s.gameplay == nil {
		return "unavailable", nil
	}
	if s.eventOf(actor.CharacterID) != nil {
		return "busy", nil
	}
	party := s.partySnapshotOf(actor.CharacterID)
	if party.LeaderID != actor.CharacterID {
		return "not_leader", nil
	}
	roster, group, reason := s.ghostRosterAt(actor.ChannelID, party, content.GhostLobby)
	if reason != "" {
		return reason, nil
	}
	def := room.Event()
	slot := eventKey{actor.ChannelID, def.Name}
	s.events.mu.Lock()
	inst := s.events.running[slot]
	if inst != nil {
		s.events.mu.Unlock()
		return s.ghostChallenge(inst, roster), nil
	}
	for _, id := range roster.members {
		if s.events.byMember[id] != nil {
			s.events.mu.Unlock()
			return "busy", nil
		}
	}
	match := &ghostMatch{room: room, team: [2]ghostRoster{roster}, moving: true, total: make(map[int64]int64)}
	deadline := time.Now().Add(3 * time.Minute)
	match.deadline.Store(deadline.UnixNano())
	match.minimum.Store(content.GhostTeamSize)
	inst = &eventInstance{id: platform.NewID(), definition: def, channel: actor.ChannelID, partyID: party.ID,
		members: make(map[int64]struct{}), deadline: deadline, properties: make(map[string]string), ghost: match}
	s.events.running[slot] = inst
	for _, id := range roster.members {
		inst.members[id] = struct{}{}
		s.events.byMember[id] = inst
	}
	s.events.mu.Unlock()
	if err := s.gameplay.resetEventRooms(inst.channel, def); err != nil {
		s.eventEnd(inst)
		return "unavailable", nil
	}
	if err := s.gameplay.prepareGhostDecks(inst.channel, room); err != nil {
		s.eventEnd(inst)
		return "unavailable", nil
	}
	if err := s.ghostWarpGroup(ctx, inst, content.GhostLobby, group, room.Waiting(), "", actor.CharacterID); err != nil {
		s.eventEnd(inst)
		return "unavailable", err
	}
	match.mu.Lock()
	match.moving = false
	match.mu.Unlock()
	return "", nil
}

func (s *Server) ghostChallenge(inst *eventInstance, team ghostRoster) string {
	match := inst.ghost
	if match == nil {
		return "busy"
	}
	match.mu.Lock()
	if match.phase != ghostWaiting || match.moving || match.challenge.partyID != 0 || team.partyID == match.team[0].partyID || !time.Now().Before(inst.ghostEndTime()) {
		match.mu.Unlock()
		return "busy"
	}
	match.challenge = team
	leader := match.team[0].leader
	match.mu.Unlock()
	for _, p := range s.eventOccupants(inst.channel, []int{match.room.Waiting()}) {
		if partySession(p.live).CharacterID == leader {
			_ = p.sink.WritePacket(protocol.PortalTrainingMessage("有另一支隊伍提出挑戰，請和登記員交談。"))
		}
	}
	return "challenged"
}

func (inst *eventInstance) ghostEndTime() time.Time {
	if inst.ghost == nil {
		return inst.deadline
	}
	return time.Unix(0, inst.ghost.deadline.Load())
}

func (s *Server) ghostWarpGroup(ctx context.Context, inst *eventInstance, from int, group []eventOccupant, to int, portalName string, speakingID int64) error {
	destination, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return err
	}
	entry, ok := destination.Spawn()
	if portalName != "" {
		entry, ok = destination.Portal(portalName)
	}
	if !ok {
		return domain.ErrNotFound
	}
	for _, p := range group {
		if partySession(p.live).CharacterID != speakingID {
			s.cancelNPCScriptState(p.live, false)
			s.closeEconomyDialog(p.live)
		}
		if err = s.warpEventMember(ctx, inst, from, p, to, entry); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) ghostHUD(inst *eventInstance, member eventOccupant) {
	g := inst.ghost
	actor := partySession(member.live)
	g.mu.Lock()
	team, ok := g.teamOf(actor.CharacterID)
	phase, stage, damage, points := g.phase, g.stage, g.damage, g.points
	g.mu.Unlock()
	if !ok {
		return
	}
	if phase < ghostRace {
		_ = member.sink.WritePacket(protocol.SessionValue("PRaid_Team", ""))
		return
	}
	_ = member.sink.WritePacket(protocol.SessionValue("PRaid_Team", ghostTeamNames[team]))
	_ = member.sink.WritePacket(protocol.PartyValue("PRaid_Point", strconv.FormatInt(points[team], 10)))
	for i, key := range [...]string{"Red_Stage", "Blue_Stage"} {
		value := strconv.Itoa(stage[i] + 1)
		if phase == ghostBoss {
			value = "B"
		}
		if phase == ghostFinished {
			value = "BC"
		}
		_ = member.sink.WritePacket(protocol.MapValue(key, value))
	}
	if phase >= ghostBoss {
		_ = member.sink.WritePacket(protocol.MapValue("redTeamDamage", strconv.FormatInt(damage[0], 10)))
		_ = member.sink.WritePacket(protocol.MapValue("blueTeamDamage", strconv.FormatInt(damage[1], 10)))
	}
}

func (s *Server) ghostAccept(ctx context.Context, actor domain.CharacterSession, accept bool) (string, error) {
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ghost == nil {
		return "unavailable", nil
	}
	match := inst.ghost
	match.mu.Lock()
	if match.phase != ghostWaiting || match.moving || match.team[0].leader != actor.CharacterID || actor.Character.MapID != match.room.Waiting() || !time.Now().Before(inst.ghostEndTime()) {
		match.mu.Unlock()
		return "unavailable", nil
	}
	proposal := match.challenge
	if proposal.partyID == 0 {
		match.mu.Unlock()
		return "waiting", nil
	}
	if !accept {
		match.challenge = ghostRoster{}
		match.mu.Unlock()
		return "declined", nil
	}
	redParty := s.partySnapshotOf(actor.CharacterID)
	blueParty := s.partySnapshotOf(proposal.leader)
	red, _, redReason := s.ghostRosterAt(inst.channel, redParty, match.room.Waiting())
	blue, blueScene, blueReason := s.ghostRosterAt(inst.channel, blueParty, content.GhostLobby)
	if redReason != "" || blueReason != "" || !sameGhostRoster(red, match.team[0]) || !sameGhostRoster(blue, proposal) {
		match.challenge = ghostRoster{}
		match.mu.Unlock()
		return "unavailable", nil
	}
	s.events.mu.Lock()
	if inst.ended || len(inst.members) != content.GhostTeamSize {
		s.events.mu.Unlock()
		match.mu.Unlock()
		return "unavailable", nil
	}
	for _, id := range blue.members {
		if s.events.byMember[id] != nil {
			s.events.mu.Unlock()
			match.mu.Unlock()
			return "unavailable", nil
		}
	}
	for _, id := range blue.members {
		inst.members[id] = struct{}{}
		s.events.byMember[id] = inst
	}
	match.team[1] = blue
	match.challenge = ghostRoster{}
	match.phase = ghostWarmup
	match.moving = true
	match.minimum.Store(content.GhostTeamSize * content.GhostTeams)
	const warmup = 10 * time.Second
	match.deadline.Store(time.Now().Add(warmup + time.Duration(inst.definition.Seconds)*time.Second).UnixNano())
	s.events.mu.Unlock()
	match.mu.Unlock()
	if err := s.ghostWarpGroup(ctx, inst, content.GhostLobby, blueScene, match.room.Waiting(), "", 0); err != nil {
		s.eventEnd(inst)
		return "unavailable", err
	}
	for _, p := range s.activeEventMembers(inst, match.room.Waiting()) {
		_ = p.sink.WritePacket(protocol.Clock(int(warmup / time.Second)))
	}
	time.AfterFunc(warmup, func() { s.startGhostRace(inst) })
	return "", nil
}

func (s *Server) startGhostRace(inst *eventInstance) {
	match := inst.ghost
	group := s.activeEventMembers(inst, match.room.Waiting())
	if len(group) != content.GhostTeamSize*content.GhostTeams {
		s.eventEnd(inst)
		return
	}
	match.mu.Lock()
	match.phase = ghostRace
	match.deadline.Store(time.Now().Add(time.Duration(inst.definition.Seconds) * time.Second).UnixNano())
	teams := match.team
	match.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for i, team := range teams {
		roster, peers, reason := s.ghostRosterAt(inst.channel, s.partySnapshotOf(team.leader), match.room.Waiting())
		if reason != "" || !sameGhostRoster(roster, team) {
			s.eventEnd(inst)
			return
		}
		if err := s.ghostWarpGroup(ctx, inst, match.room.Waiting(), peers, match.room.Course(i, 0), ghostTeamNames[i], 0); err != nil {
			s.eventEnd(inst)
			return
		}
	}
	match.mu.Lock()
	match.moving = false
	match.mu.Unlock()
	s.ghostBroadcast(inst)
}

func sameGhostRoster(a, b ghostRoster) bool {
	if a.partyID != b.partyID || a.leader != b.leader {
		return false
	}
	for _, id := range a.members {
		if !b.contains(id) {
			return false
		}
	}
	return true
}

// The waiting leader can accept or decline the other party's request through the
// original registrar. Waiting parties can always leave through the registrar.
func (s *Server) ghostWaitingStatus(actor domain.CharacterSession) string {
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ghost == nil {
		return "unavailable"
	}
	match := inst.ghost
	match.mu.Lock()
	defer match.mu.Unlock()
	if match.phase != ghostWaiting || actor.Character.MapID != match.room.Waiting() {
		return "unavailable"
	}
	if actor.CharacterID == match.team[0].leader && match.challenge.partyID != 0 {
		return "challenge"
	}
	return "waiting"
}

func (s *Server) ghostBroadcast(inst *eventInstance) {
	for _, member := range s.eventOccupants(inst.channel, inst.definition.Maps) {
		if s.eventOf(partySession(member.live).CharacterID) == inst {
			s.ghostHUD(inst, member)
		}
	}
}

func (inst *eventInstance) belongsToGhostParty(id int64) bool {
	if inst.ghost == nil || id == 0 {
		return false
	}
	g := inst.ghost
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.team[0].partyID == id || g.team[1].partyID == id
}
