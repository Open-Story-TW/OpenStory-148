package world

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"os"
	"sort"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

const (
	carnivalRed  = 0
	carnivalBlue = 1
)

var carnivalTaiwanTime = time.FixedZone("Asia/Taipei", 8*60*60)

// Carnival state is scoped to a channel and an original arena slot.
// gameplay.mu owns the registry and the scores shared with combat settlement.
type carnivalState struct {
	invitation time.Time
	available  bool
	entrants   map[int64]*carnivalPlayer
	lastInvite map[int64]time.Time
	legacy     map[mapKey]*carnivalLegacyQueue
}

type carnivalPlayer struct {
	owner       *sceneVisit
	home        int
	admission   time.Time
	waitDone    chan struct{}
	waitMap     int
	match       *carnivalMatch
	team        byte
	available   int
	earned      int
	dead        bool
	legacy      bool
	legacyQueue *carnivalLegacyQueue
}

type carnivalMatch struct {
	key       mapKey
	members   []*carnivalPlayer
	arena     content.CarnivalField
	ready     bool
	canceled  bool
	endsAt    time.Time
	changesAt time.Time
	totals    [2]int
	extra     bool
	collected bool
	legacyID  string
}

func (m *carnivalMatch) revivalMap() int {
	if m.legacyID != "" {
		return m.key.mapID + 100
	}
	return m.key.mapID + 1
}

func (m *carnivalMatch) exitMap() int {
	if m.legacyID != "" {
		return content.CarnivalSecondExit
	}
	return content.CarnivalExit
}

func carnivalInvitationTime(now time.Time) time.Time {
	local := now.In(carnivalTaiwanTime)
	minute := local.Minute()
	if minute != 15 && minute != 45 {
		return time.Time{}
	}
	// Gamania's V143 launch schedule ran daily between 16:00 and 20:30.
	// The WZ retains :15/:45 in V148 and the intervening notes leave the
	// hours in place. The final invitation is at 20:15 Taiwan time.
	minuteOfDay := local.Hour()*60 + minute
	if minuteOfDay < 16*60 || minuteOfDay >= 20*60+30 {
		return time.Time{}
	}
	return now.Truncate(time.Minute)
}

func (s *Server) carnivalCanInvite(session domain.CharacterSession) bool {
	c := session.Character
	return c.Level >= content.CarnivalMinimumLevel && c.HP > 0 &&
		!content.CarnivalMatchField(c.MapID) && !content.CarnivalSecondField(c.MapID) &&
		c.MapID != content.CarnivalExit && s.eventOf(c.ID) == nil
}

func (s *Server) acceptCarnivalQuest(ctx context.Context, channel int, live *liveCharacter, sink packetSink, request protocol.QuestActionRequest) error {
	session := partySession(live)
	visit := s.sceneFor(live, sink)
	now := time.Now()
	window := carnivalInvitationTime(now)
	if request.NPCID != content.CarnivalNPC || request.Action != 4 || visit == nil ||
		!s.carnivalCanInvite(session) || window.IsZero() || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	g := s.gameplay
	if g == nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	g.mu.Lock()
	state := g.carnival
	if state == nil || !state.available || state.lastInvite[session.CharacterID] != window || state.entrants[session.CharacterID] != nil {
		g.mu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	waitMap := content.CarnivalWaitingFirst + (window.Minute()/30)%2
	p := &carnivalPlayer{owner: visit, home: session.Character.MapID, admission: window, waitMap: waitMap, waitDone: make(chan struct{})}
	state.entrants[session.CharacterID] = p
	g.mu.Unlock()
	defer close(p.waitDone)
	if !s.carnivalWarp(ctx, p, visit.owner.Character.MapID, waitMap, "") {
		g.mu.Lock()
		if state.entrants[session.CharacterID] == p {
			delete(state.entrants, session.CharacterID)
		}
		g.mu.Unlock()
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	return nil
}

func (g *gameplaySpace) carnivalEntryLocked(session domain.CharacterSession, mapID int) bool {
	legacy := content.CarnivalSecondField(mapID)
	if !legacy && !content.CarnivalMatchField(mapID) {
		return true
	}
	if g.carnival == nil || legacy && g.carnival.legacy == nil {
		return false
	}
	p := g.carnival.entrants[session.CharacterID]
	if p == nil || p.legacy != legacy || !sameSceneOwner(p.owner.owner, session) {
		return false
	}
	if legacy && (mapID == content.CarnivalSecondLobby || mapID == content.CarnivalSecondExit) {
		return true
	}
	if p.match == nil {
		return p.waitMap == mapID
	}
	m := p.match
	return !m.canceled && (mapID == m.key.mapID || mapID == m.revivalMap() ||
		mapID == m.arena.WinnerMap || mapID == m.arena.LoserMap)
}

func (s *Server) carnivalWarp(ctx context.Context, p *carnivalPlayer, from, to int, portal string) bool {
	snapshot := partySession(p.owner.live)
	if snapshot.Character.MapID != from || !sameSceneOwner(p.owner.owner, snapshot) {
		return false
	}
	m, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return false
	}
	var entry content.Portal
	var ok bool
	if portal == "" {
		entry, ok = m.Spawn()
	} else {
		entry, ok = m.Portal(portal)
	}
	if !ok {
		return false
	}
	var revive *revivalTransition
	if snapshot.Character.HP == 0 {
		revive = &revivalTransition{fromMap: from, hp: 50}
	}
	guard := func(current domain.CharacterSession) bool {
		return sameSceneOwner(p.owner.owner, current) && current.Character.MapID == from &&
			(current.Character.HP == 0) == (revive != nil)
	}
	err = s.warpGameplayConditional(ctx, snapshot.ChannelID, p.owner.live, p.owner.sink, to, entry, revive, guard, nil)
	return err == nil && partySession(p.owner.live).Character.MapID == to
}

func (g *gameplaySpace) prepareCarnival() {
	g.carnival = &carnivalState{entrants: make(map[int64]*carnivalPlayer), lastInvite: make(map[int64]time.Time)}
	if os.Getenv("MAPLEGO_CARNIVAL_II_COMPAT") == "1" {
		g.carnival.legacy = make(map[mapKey]*carnivalLegacyQueue)
	}
	for _, id := range []int{content.CarnivalWaitingFirst, content.CarnivalWaitingLast, content.CarnivalExit, content.CarnivalArenaFirst} {
		m, err := g.catalog.Map(id)
		if err != nil || id == content.CarnivalArenaFirst && m.Carnival == nil {
			return
		}
	}
	g.carnival.available = true
}

func (s *Server) offerCarnivalInvitations(now time.Time) {
	window := carnivalInvitationTime(now)
	if window.IsZero() {
		return
	}
	var visits []*sceneVisit
	s.mu.RLock()
	for _, online := range s.scenes {
		visits = append(visits, online)
	}
	s.mu.RUnlock()
	g := s.gameplay
	for _, candidate := range visits {
		snapshot := partySession(candidate.live)
		if !s.carnivalCanInvite(snapshot) || !s.sceneCurrent(candidate) {
			continue
		}
		g.mu.Lock()
		state := g.carnival
		if state == nil || !state.available || state.lastInvite[snapshot.CharacterID] == window {
			g.mu.Unlock()
			continue
		}
		state.lastInvite[snapshot.CharacterID] = window
		g.mu.Unlock()
		expires := int(window.Add(time.Minute).Sub(time.Now()).Milliseconds())
		if expires > 0 {
			_ = s.writeScenePackets(candidate, protocol.TimedQuestInvitation(content.CarnivalInvitationQuest, expires))
		}
	}
}

func (s *Server) closeCarnivalAdmission(ctx context.Context, window time.Time) {
	g := s.gameplay
	g.mu.Lock()
	var waiting []*carnivalPlayer
	for _, p := range g.carnival.entrants {
		if p.match == nil && p.admission == window {
			waiting = append(waiting, p)
		}
	}
	g.mu.Unlock()
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].owner.owner.CharacterID < waiting[j].owner.owner.CharacterID })
	channels := make(map[int][]*carnivalPlayer)
	for _, p := range waiting {
		select {
		case <-p.waitDone:
		case <-ctx.Done():
			return
		}
		c := partySession(p.owner.live)
		if c.Character.MapID == p.waitMap && c.Character.HP > 0 && s.sceneFor(p.owner.live, p.owner.sink) != nil {
			channels[c.ChannelID] = append(channels[c.ChannelID], p)
		}
	}
	for channel, entrants := range channels {
		for len(entrants) >= content.CarnivalWaitingMinimum {
			match := g.reserveCarnivalArena(channel, entrants[:content.CarnivalWaitingMinimum])
			if match == nil {
				break
			}
			s.startCarnivalMatch(ctx, match)
			entrants = entrants[content.CarnivalWaitingMinimum:]
		}
		for _, p := range entrants {
			if visit := s.sceneFor(p.owner.live, p.owner.sink); visit != nil {
				_ = s.writeScenePackets(visit, protocol.Notice("怪物擂台賽人數不足。"))
			}
			s.carnivalWarp(ctx, p, p.waitMap, content.CarnivalExit, "")
		}
	}
}

func (g *gameplaySpace) reserveCarnivalArena(channel int, group []*carnivalPlayer) *carnivalMatch {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range group {
		if g.carnival.entrants[p.owner.owner.CharacterID] != p || p.match != nil {
			return nil
		}
	}
	for arena := content.CarnivalArenaFirst; arena <= content.CarnivalArenaLast; arena += 100 {
		key := mapKey{channel: channel, mapID: arena}
		room, err := g.room(key)
		if err != nil || room.carnival != nil || len(room.members) > 0 || room.definition.Carnival == nil {
			continue
		}
		if _, ok := room.definition.Portal("red00"); !ok {
			continue
		}
		if _, ok := room.definition.Portal("blue00"); !ok {
			continue
		}
		members := append([]*carnivalPlayer(nil), group...)
		rand.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
		m := &carnivalMatch{key: key, members: members, arena: *room.definition.Carnival}
		for i, p := range members {
			p.match, p.team = m, byte(i/content.CarnivalTeamSize)
		}
		room.carnival = m
		return m
	}
	return nil
}

func (s *Server) startCarnivalMatch(ctx context.Context, m *carnivalMatch) {
	g := s.gameplay
	for _, p := range m.members {
		portal := "red00"
		if p.team == carnivalBlue {
			portal = "blue00"
		}
		if !s.carnivalWarp(ctx, p, p.waitMap, m.key.mapID, portal) {
			g.mu.Lock()
			m.canceled = true
			g.mu.Unlock()
			break
		}
	}
	g.mu.Lock()
	room := g.rooms[m.key]
	if len(room.members) != len(m.members) {
		m.canceled = true
	}
	m.ready = !m.canceled
	if m.ready {
		m.endsAt = time.Now().Add(time.Duration(m.arena.TimeLimit) * time.Second)
	}
	destinations := make([]*sceneVisit, 0, len(room.members))
	for _, participant := range room.members {
		destinations = append(destinations, participant.visit)
	}
	g.mu.Unlock()
	if m.canceled {
		s.leaveCarnivalMatch(ctx, m)
		return
	}
	for _, visit := range destinations {
		s.publishCarnivalEntry(visit)
	}
	slog.Info("carnival_match_started", "arena", m.key.mapID, "channel", m.key.channel, "players", len(destinations))
}

func (s *Server) publishCarnivalEntry(visit *sceneVisit) {
	g := s.gameplay
	g.mu.Lock()
	packets := g.carnivalEntryPacketsLocked(visit)
	g.mu.Unlock()
	_ = s.writeScenePackets(visit, packets...)
}

func (g *gameplaySpace) carnivalEntryPacketsLocked(visit *sceneVisit) [][]byte {
	if visit == nil || g.carnival == nil || content.CarnivalArenaMode(visit.owner.Character.MapID) == 0 {
		return nil
	}
	p := g.carnival.entrants[visit.owner.CharacterID]
	if p == nil || p.match == nil || !p.match.ready || p.match.canceled || p.match.collected || !sameSceneOwner(p.owner.owner, visit.owner) {
		return nil
	}
	m := p.match
	p.dead = false
	start := protocol.CarnivalStart(p.team, p.available, p.earned, m.totals[p.team], m.totals[1-p.team], len(m.arena.Summons))
	seconds := max(0, int((time.Until(m.endsAt)+time.Second-1)/time.Second))
	return [][]byte{start, protocol.CarnivalCooldowns([4]int{}), protocol.Clock(seconds)}
}

func (s *Server) tickCarnival(ctx context.Context, now time.Time) {
	g := s.gameplay
	if g.carnival == nil || !g.carnival.available && g.carnival.legacy == nil {
		return
	}
	if g.carnival.legacy != nil {
		s.expireCarnivalLegacyQueues(ctx, now)
	}
	if g.carnival.available {
		window := carnivalInvitationTime(now)
		previous := g.carnival.invitation
		if previous != window && !previous.IsZero() {
			g.mu.Lock()
			clear(g.carnival.lastInvite)
			g.mu.Unlock()
			go s.closeCarnivalAdmission(ctx, previous)
		}
		g.carnival.invitation = window
		if !window.IsZero() {
			s.offerCarnivalInvitations(now)
		}
	}

	var transitions []*carnivalMatch
	var deliveries []carnivalDelivery
	g.mu.Lock()
	for _, room := range g.rooms {
		m := room.carnival
		if m == nil || !m.ready || m.canceled || m.collected {
			continue
		}
		if !m.changesAt.IsZero() {
			if !now.Before(m.changesAt) {
				m.collected = true
				transitions = append(transitions, m)
			}
			continue
		}
		if now.Before(m.endsAt) {
			continue
		}
		if m.totals[carnivalBlue] == m.totals[carnivalRed] && !m.extra && m.arena.TimeExtra > 0 {
			m.extra = true
			m.endsAt = now.Add(time.Duration(m.arena.TimeExtra) * time.Second)
			deliveries = append(deliveries, carnivalBroadcastLocked(room, protocol.Clock(m.arena.TimeExtra))...)
			continue
		}
		m.changesAt = now.Add(time.Duration(m.arena.ResultDelay) * time.Second)
		ranks := make([]protocol.CarnivalRank, 0, len(m.members))
		for _, p := range m.members {
			ranks = append(ranks, protocol.CarnivalRank{CharacterID: p.owner.owner.CharacterID, Name: p.owner.owner.Character.Name, Points: p.earned, Team: p.team})
		}
		sort.SliceStable(ranks, func(i, j int) bool { return ranks[i].Points > ranks[j].Points })
		ranking := protocol.CarnivalRanking(ranks)
		for id, current := range room.members {
			p := g.carnival.entrants[id]
			if p == nil || p.match != m {
				continue
			}
			effect, sound := m.arena.LoseEffect, m.arena.LoseSound
			if m.totals[p.team] > m.totals[1-p.team] {
				effect, sound = m.arena.WinEffect, m.arena.WinSound
			}
			deliveries = append(deliveries, carnivalDelivery{current.visit, [][]byte{protocol.FieldScreenEffect(effect), protocol.FieldSoundEffect(sound), ranking}})
		}
		slog.Info("carnival_match_result", "arena", m.key.mapID, "red", m.totals[carnivalRed], "blue", m.totals[carnivalBlue])
	}
	g.mu.Unlock()
	s.publishCarnivalDeliveries(deliveries)
	for _, done := range transitions {
		go s.leaveCarnivalMatch(ctx, done)
	}
}

func (s *Server) leaveCarnivalMatch(ctx context.Context, m *carnivalMatch) {
	g := s.gameplay
	for _, p := range m.members {
		snapshot := partySession(p.owner.live)
		from := snapshot.Character.MapID
		if from != m.key.mapID && from != m.revivalMap() && from != p.waitMap {
			continue
		}
		to := m.exitMap()
		if !m.canceled {
			to = m.arena.LoserMap
			if m.totals[p.team] > m.totals[1-p.team] {
				to = m.arena.WinnerMap
			}
		}
		if !s.carnivalWarp(ctx, p, from, to, "") {
			s.carnivalWarp(ctx, p, from, m.exitMap(), "")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.rooms[m.key]; r != nil && r.carnival == m && len(r.members) == 0 {
		delete(g.rooms, m.key)
	}
}

func (s *Server) restoreCarnivalLobby(ctx context.Context, snapshot *domain.CharacterSession) error {
	if !content.CarnivalMatchField(snapshot.Character.MapID) && !content.CarnivalSecondField(snapshot.Character.MapID) &&
		snapshot.Character.MapID != content.CarnivalExit {
		return nil
	}
	if s.gameplay == nil || s.gameplay.carnival == nil {
		return nil
	}
	town, err := s.gameplay.catalog.Map(100000000)
	if err != nil {
		return err
	}
	spawn, ok := town.Spawn()
	if !ok {
		return domain.ErrNotFound
	}
	value := snapshot.Character
	value.MapID, value.SpawnPoint, value.X, value.Y = town.ID, spawn.ID, spawn.X, spawn.Y
	value.HP = max(1, value.HP)
	if err := s.repo.SaveCharacter(ctx, *snapshot, value); err != nil {
		return err
	}
	value.Revision++
	snapshot.Character = value
	s.gameplay.mu.Lock()
	delete(s.gameplay.carnival.entrants, snapshot.CharacterID)
	s.gameplay.mu.Unlock()
	return nil
}

func (g *gameplaySpace) leaveCarnivalSession(visit *sceneVisit) {
	if g.carnival == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cid := visit.owner.CharacterID
	p := g.carnival.entrants[cid]
	if p != nil && sameSceneOwner(p.owner.owner, visit.owner) && sameDropSink(p.owner.sink, visit.sink) {
		delete(g.carnival.entrants, cid)
	}
}

func (s *Server) carnivalRevivePortal(ctx context.Context, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	snapshot := partySession(live)
	mapID := snapshot.Character.MapID
	if content.CarnivalArenaMode(mapID-1) != 1 && content.CarnivalArenaMode(mapID-100) != 2 {
		return false, nil
	}
	g := s.gameplay
	g.mu.Lock()
	p := g.carnival.entrants[snapshot.CharacterID]
	valid := portal.Name == "pt00" && p != nil && p.match != nil &&
		p.match.revivalMap() == mapID && p.match.changesAt.IsZero() && !p.match.canceled &&
		sameSceneOwner(p.owner.owner, snapshot) && sameDropSink(p.owner.sink, sink)
	var entry string
	if valid {
		entry = "red_revive"
		if p.team == carnivalBlue {
			entry = "blue_revive"
		}
	}
	g.mu.Unlock()
	if !valid || !s.carnivalWarp(ctx, p, mapID, p.match.key.mapID, entry) {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	return true, nil
}

func (s *Server) carnivalRevive(ctx context.Context, live *liveCharacter, sink packetSink, snapshot domain.CharacterSession) error {
	g := s.gameplay
	g.mu.Lock()
	p := g.carnival.entrants[snapshot.CharacterID]
	room := g.rooms[mapKey{channel: snapshot.ChannelID, mapID: snapshot.Character.MapID}]
	if room == nil || room.carnival == nil || p == nil || p.match != room.carnival ||
		!sameSceneOwner(p.owner.owner, snapshot) || !sameDropSink(p.owner.sink, sink) {
		g.mu.Unlock()
		return sink.WritePacket(protocol.UpdateHPMP(snapshot.Character.HP, snapshot.Character.MP))
	}
	var deliveries []carnivalDelivery
	if !p.dead && p.match.changesAt.IsZero() {
		p.dead = true
		lost := min(p.available, p.match.arena.DeathCost)
		p.available -= lost
		deliveries = carnivalBroadcastLocked(room, protocol.CarnivalDeath(p.team, snapshot.Character.Name, lost))
	}
	g.mu.Unlock()
	s.publishCarnivalDeliveries(deliveries)
	s.carnivalWarp(ctx, p, snapshot.Character.MapID, p.match.revivalMap(), "")
	return nil
}

func (a *worldScript) leaveCarnivalNPC(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	result := scripting.CommandResult{Value: false}
	if a.npcID != 2042002 || command.Script != "npc/2042002.lua" {
		return result, domain.ErrForbidden
	}
	s := a.server
	g := s.gameplay
	mapID := snapshot.Character.MapID
	if g == nil || g.carnival == nil || !content.CarnivalMatchField(mapID) && mapID != content.CarnivalExit {
		return result, domain.ErrForbidden
	}
	g.mu.Lock()
	p := g.carnival.entrants[snapshot.CharacterID]
	ok := p != nil && sameSceneOwner(p.owner.owner, snapshot) && sameDropSink(p.owner.sink, a.sink)
	g.mu.Unlock()
	if !ok {
		return result, nil
	}
	to := content.CarnivalExit
	if mapID == content.CarnivalExit {
		to = p.home
	}
	if !s.carnivalWarp(ctx, p, mapID, to, "") {
		return result, nil
	}
	if to == p.home {
		g.mu.Lock()
		if g.carnival.entrants[snapshot.CharacterID] == p {
			delete(g.carnival.entrants, snapshot.CharacterID)
		}
		g.mu.Unlock()
	}
	result.Value = true
	return result, nil
}
