package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

// An event instance is one group inside the stage maps of a content.Event.
// The stage maps are the channel's ordinary maps, kept for that group until
// the instance ends: by its time limit, by too few members remaining, or by a
// script finishing it. Instances live in memory only. After a restart nobody
// belongs to one, and a character who logs in on a stage map is sent to the
// event's exit map (eventEntered).
type eventKey struct {
	channel int
	name    string
}

type eventInstance struct {
	id           string
	definition   content.Event
	channel      int
	partyID      int64
	expeditionID int64
	members      map[int64]struct{}
	deadline     time.Time
	preludeUntil time.Time
	summoned     bool
	properties   map[string]string
	starting     bool
	cleared      bool
	ended        bool
	moon         *henesysState
	ludi         *ludibriumState
	dragon       *dragonRiderState
	ice          *iceKnightState
	christmas    christmasProgress
	hoblin       *hoblinPQState
	romeoSearch  *romeoLabSearch
	romeoLanes   *romeoLabLanes
	romeoArchive romeoLabArchive
	romeoAngered bool
}

type eventSpace struct {
	mu       sync.Mutex
	running  map[eventKey]*eventInstance
	byMember map[int64]*eventInstance
}

func newEventSpace() *eventSpace {
	return &eventSpace{running: map[eventKey]*eventInstance{}, byMember: map[int64]*eventInstance{}}
}

const (
	eventPropertyLimit = 64
	eventValueLimit    = 256
)

// eventOccupant is anyone standing on a stage map, member or not.
type eventOccupant struct {
	live *liveCharacter
	sink packetSink
}

func (s *Server) eventOccupants(channel int, maps []int) []eventOccupant {
	if s.gameplay == nil {
		return nil
	}
	var out []eventOccupant
	s.gameplay.mu.Lock()
	for _, id := range maps {
		if room := s.gameplay.rooms[mapKey{channel: channel, mapID: id}]; room != nil {
			for _, m := range room.members {
				out = append(out, eventOccupant{m.live, m.sink})
			}
		}
	}
	s.gameplay.mu.Unlock()
	return out
}

// eventStatus reports whether name exists and whether a group holds it on
// this channel.
func (s *Server) eventStatus(channel int, name string) map[string]any {
	e, known := content.EventByName(name)
	value := map[string]any{"known": known, "busy": false, "members": int64(0)}
	if !known || s.events == nil {
		return value
	}
	s.events.mu.Lock()
	if inst := s.events.running[eventKey{channel, name}]; inst != nil {
		value["busy"], value["members"] = true, int64(len(inst.members))
	}
	s.events.mu.Unlock()
	if value["busy"] == false && len(s.eventOccupants(channel, e.Maps)) > 0 {
		value["busy"] = true
	}
	return value
}

// eventStart moves the leader's party or an eligible solo participant into the event. The reason is "" on
// success, otherwise one of unknown, no_party, not_leader, party_size, level
// or busy; here and qualified count the members on the leader's map and the
// ones among them inside the level range, for the NPC's refusal text.
func (s *Server) eventStart(ctx context.Context, name string, channel int, live *liveCharacter, sink packetSink) (reason string, here, qualified int, err error) {
	e, ok := content.EventByName(name)
	if !ok || s.events == nil || s.gameplay == nil || s.parties == nil {
		return "unknown", 0, 0, nil
	}
	destination, err := s.gameplay.catalog.Map(e.Maps[0])
	if err != nil {
		return "unknown", 0, 0, nil
	}
	var entry content.Portal
	found := false
	for _, p := range destination.Portals {
		if p.ID == e.EntryPortal {
			entry, found = p, true
		}
	}
	if !found {
		return "unknown", 0, 0, nil
	}
	live.mu.Lock()
	leader := live.session
	live.mu.Unlock()
	if leader.ChannelID != channel || leader.Character.HP <= 0 || s.tradeActive(live) || e.LobbyMap != 0 && leader.Character.MapID != e.LobbyMap {
		return "unavailable", 0, 0, nil
	}
	// Reserve the expedition while its roster cannot change. The map warps
	// below must run after the command lock is released.
	if e.ExpeditionType != 0 {
		s.parties.commands.Lock()
	}
	inst, present, reason, here, qualified := s.eventReserve(e, leader, live)
	if e.ExpeditionType != 0 {
		s.parties.commands.Unlock()
	}
	if reason != "" {
		return reason, here, qualified, nil
	}
	key := eventKey{channel, name}
	if err = s.gameplay.resetEventRooms(channel, e); err != nil {
		slog.Warn("event_reset_failed", "event", name, "channel", channel, "error", err)
		s.events.mu.Lock()
		s.closeEventMembersLocked(inst)
		if s.events.running[key] == inst {
			delete(s.events.running, key)
		}
		s.events.mu.Unlock()
		return "unavailable", here, qualified, nil
	}
	if inst.moon != nil {
		ready := append(append([]eventOccupant(nil), present...), eventOccupant{live, sink})
		if err = s.prepareHenesysPQ(ctx, inst, ready); err != nil {
			s.eventEnd(inst)
			return "unavailable", here, qualified, err
		}
	}
	if inst.ludi != nil {
		if repo, ok := s.repo.(domain.EventDropRepository); ok {
			if err = repo.ExpireEventDrops(ctx, s.cfg.WorldID, channel, content.LudiPQQuiz); err != nil {
				s.eventEnd(inst)
				return "unavailable", here, qualified, err
			}
		}
	}
	if inst.ice != nil || inst.hoblin != nil {
		totalLevel := leader.Character.Level
		for _, m := range present {
			totalLevel += partySession(m.live).Character.Level
		}
		hp := int64(totalLevel/here) * 1000
		if inst.ice != nil {
			err = s.gameplay.prepareIceEscort(inst, hp)
		} else {
			err = s.gameplay.prepareHoblinPQ(inst, hp)
		}
		if err != nil {
			s.eventEnd(inst)
			return "unavailable", here, qualified, err
		}
	}
	leaderOccupant := eventOccupant{live, sink}
	if content.RomeoJulietEvent(name) {
		group := append(append([]eventOccupant(nil), present...), leaderOccupant)
		if err := s.clearRomeoSupplies(ctx, inst, group); err != nil {
			s.eventEnd(inst)
			return "unavailable", here, qualified, nil
		}
	}
	var groupToCharge []eventOccupant
	if e.Ticket != 0 {
		groupToCharge = append(append([]eventOccupant{}, present...), leaderOccupant)
		err = s.exchangeEventItems(ctx, inst, groupToCharge, leader.Character.MapID, "ticket", e.Ticket, -1)
		if err != nil {
			s.eventEnd(inst)
			if errors.Is(err, domain.ErrInsufficient) {
				return "ticket", here, qualified, nil
			}
			return "unavailable", here, qualified, nil
		}
	}
	var failed []eventOccupant
	for _, m := range present {
		s.cancelNPCScriptState(m.live, false)
		s.closeEconomyDialog(m.live)
		// A member who cannot be moved stays outside and is no member.
		if s.warpEventMember(ctx, inst, leader.Character.MapID, m, destination.ID, entry) != nil {
			s.eventUnregister(inst, partySession(m.live).CharacterID)
			failed = append(failed, m)
		}
	}
	err = s.warpEventMember(ctx, inst, leader.Character.MapID, leaderOccupant, destination.ID, entry)
	if e.Ticket != 0 {
		if err != nil || here-len(failed) < e.PartyMin {
			failed = groupToCharge
		}
		for _, member := range failed {
			if refundErr := s.exchangeEventItems(ctx, inst, []eventOccupant{member}, 0, "refund", e.Ticket, 1); refundErr != nil {
				slog.Warn("event_ticket_refund_failed", "event", e.Name, "character", partySession(member.live).CharacterID, "error", refundErr)
			}
		}
		if here-len(failed) < e.PartyMin {
			s.eventEnd(inst)
			return "unavailable", here, qualified, err
		}
	}
	if err != nil {
		s.eventEnd(inst)
		return "unavailable", here, qualified, err
	}
	s.events.mu.Lock()
	inst.starting = false
	partial := e.ExpeditionType != 0 && (inst.ended || len(inst.members) != here)
	if !partial && e.Name == "BalrogEasy" {
		inst.preludeUntil = time.Now().Add(balrogWarmupDuration)
	}
	s.events.mu.Unlock()
	if partial {
		s.eventEnd(inst)
		return "unavailable", here, qualified, nil
	}
	s.eventSettle(inst)
	if e.Name == "Cygnus" {
		s.startCygnusBattle(inst)
	}
	return "", here, qualified, err
}

func (s *Server) eventReserve(e content.Event, leader domain.CharacterSession, live *liveCharacter) (inst *eventInstance, present []eventOccupant, reason string, here, qualified int) {
	var partyID, expeditionID int64
	var size int
	var group []eventOccupant
	if e.ExpeditionType != 0 {
		expeditionID, size, group, reason = s.eventExpeditionRoster(e, leader.CharacterID, live)
	} else {
		partyID, size, group, reason = s.eventPartyRoster(leader.CharacterID)
		if reason == "no_party" && e.Ticket != 0 && e.PartyMin == 1 {
			size, reason = 1, ""
		}
	}
	if reason != "" {
		return nil, nil, reason, 0, 0
	}
	fits := func(c domain.Character) bool {
		return e.ExpeditionType == 0 && c.GM > 0 || c.Level >= e.LevelMin && c.Level <= e.LevelMax
	}
	here, qualified = 1, 0
	if fits(leader.Character) {
		qualified = 1
	}
	present = group[:0]
	for _, m := range group {
		there := partySession(m.live)
		if there.ChannelID != leader.ChannelID || there.Character.MapID != leader.Character.MapID || there.Character.HP <= 0 || s.tradeActive(m.live) {
			continue
		}
		here++
		if fits(there.Character) {
			qualified++
		}
		present = append(present, m)
	}
	if here < e.PartyMin || size > e.PartyMax || e.Ticket != 0 && here != size {
		return nil, nil, "party_size", here, qualified
	}
	if qualified != here {
		return nil, nil, "level", here, qualified
	}
	if e.ExpeditionType != 0 && here != size {
		return nil, nil, "roster_absent", here, qualified
	}
	if e.Name == content.IceKnightName && (leader.Character.MapID != content.IceKnightLobby || here != size) {
		return nil, nil, "party_size", here, qualified
	}
	if e.Name == content.HoblinPQName && ((leader.Character.MapID != content.HoblinTown && leader.Character.MapID != content.HoblinLobby) || here != size) {
		return nil, nil, "party_size", here, qualified
	}
	if e.Name == content.DragonRiderName {
		if leader.Character.MapID != content.DragonRiderGate || here != size {
			return nil, nil, "party_size", here, qualified
		}
		if !knowsSoaring(leader.Character) {
			return nil, nil, "soaring", here, qualified
		}
		for _, member := range present {
			if !knowsSoaring(partySession(member.live).Character) {
				return nil, nil, "soaring", here, qualified
			}
		}
	}
	if e.Name == "Zakum" || e.Name == "ChaosZakum" {
		if !qualifiedForZakum(leader.Character) {
			return nil, nil, "zakum_prerequisite", here, qualified
		}
		for _, o := range present {
			if !qualifiedForZakum(partySession(o.live).Character) {
				return nil, nil, "zakum_prerequisite", here, qualified
			}
		}
	}
	occupied := len(s.eventOccupants(leader.ChannelID, e.Maps)) > 0
	inst = &eventInstance{id: platform.NewID(), definition: e, channel: leader.ChannelID, partyID: partyID, expeditionID: expeditionID,
		members: map[int64]struct{}{leader.CharacterID: {}}, deadline: time.Now().Add(time.Duration(e.Seconds) * time.Second),
		properties: map[string]string{}, starting: expeditionID != 0}
	if e.Name == content.HenesysPQName {
		inst.moon = &henesysState{}
	}
	if e.Name == content.LudibriumPQName {
		inst.ludi = newLudibriumState()
	}
	if e.Name == content.DragonRiderName {
		inst.dragon = &dragonRiderState{rewards: map[int64]dragonRiderReward{}}
	}
	if e.Name == content.IceKnightName {
		inst.ice = &iceKnightState{rewards: make(map[int64]int64)}
	}
	if e.Name == content.HoblinPQName {
		inst.hoblin = &hoblinPQState{rewards: make(map[int64]int64)}
	}
	for _, m := range present {
		inst.members[partySession(m.live).CharacterID] = struct{}{}
	}
	key := eventKey{leader.ChannelID, e.Name}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	busy := occupied || s.events.running[key] != nil
	for id := range inst.members {
		busy = busy || s.events.byMember[id] != nil
	}
	if busy {
		return nil, nil, "busy", here, qualified
	}
	s.events.running[key] = inst
	for id := range inst.members {
		s.events.byMember[id] = inst
	}
	return inst, present, "", here, qualified
}

func (s *Server) eventPartyRoster(leader int64) (partyID int64, size int, group []eventOccupant, reason string) {
	party := s.partySnapshotOf(leader)
	if party.ID == 0 {
		return 0, 0, nil, "no_party"
	}
	if party.LeaderID != leader {
		return 0, 0, nil, "not_leader"
	}
	s.parties.mu.Lock()
	for _, m := range party.Members {
		if peer := s.parties.peers[m.CharacterID]; peer != nil && peer.party.ID == party.ID && m.CharacterID != leader {
			group = append(group, eventOccupant{peer.live, peer.sink})
		}
	}
	s.parties.mu.Unlock()
	return party.ID, len(party.Members), group, ""
}

func (s *Server) warpEventMember(ctx context.Context, inst *eventInstance, from int, member eventOccupant, mapID int, entry content.Portal) error {
	guard := func(session domain.CharacterSession) bool {
		return session.ChannelID == inst.channel && session.Character.MapID == from && session.Character.HP > 0 &&
			s.eventOf(session.CharacterID) == inst
	}
	if err := s.warpGameplayConditional(ctx, inst.channel, member.live, member.sink, mapID, entry, nil, guard, nil); err != nil {
		return err
	}
	if partySession(member.live).Character.MapID != mapID {
		return domain.ErrStaleOwner
	}
	return nil
}

func (s *Server) moveEventRoom(ctx context.Context, inst *eventInstance, from int, actor eventOccupant, to int, entry content.Portal) error {
	s.warpEventPeers(ctx, inst, from, actor.live, to, entry)
	s.cancelNPCScriptState(actor.live, false)
	s.closeEconomyDialog(actor.live)
	return s.warpEventMember(ctx, inst, from, actor, to, entry)
}

func (s *Server) warpEventPeers(ctx context.Context, inst *eventInstance, from int, actor *liveCharacter, to int, entry content.Portal) {
	for _, member := range s.eventOccupants(inst.channel, []int{from}) {
		if member.live == actor || s.eventOf(partySession(member.live).CharacterID) != inst {
			continue
		}
		s.cancelNPCScriptState(member.live, false)
		s.closeEconomyDialog(member.live)
		_ = s.warpEventMember(ctx, inst, from, member, to, entry)
	}
}

func (s *Server) activeEventMembers(inst *eventInstance, mapID int) []eventOccupant {
	members := s.eventOccupants(inst.channel, []int{mapID})
	group := members[:0]
	for _, member := range members {
		peer := partySession(member.live)
		if peer.Character.HP > 0 && s.eventOf(peer.CharacterID) == inst {
			group = append(group, member)
		}
	}
	return group
}

func eventPortalNotice(sink packetSink, text string) error {
	if err := sink.WritePacket(protocol.PortalTrainingMessage(text)); err != nil {
		return err
	}
	return sink.WritePacket(protocol.EnableActions())
}

func (s *Server) eventAllowsEntry(session domain.CharacterSession, mapID int) bool {
	if !s.treasureAllowsEntry(session, mapID) {
		return false
	}
	e, stage := content.EventByMap(mapID)
	if !stage || s.events == nil {
		return true
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	inst := s.events.running[eventKey{session.ChannelID, e.Name}]
	if inst == nil {
		return e.ExpeditionType == 0 && session.Character.GM > 0
	}
	return !inst.ended && s.events.byMember[session.CharacterID] == inst
}

// eventUnregister drops one member without moving them.
func (s *Server) eventUnregister(inst *eventInstance, id int64) {
	s.events.mu.Lock()
	if s.events.byMember[id] == inst {
		delete(s.events.byMember, id)
		delete(inst.members, id)
	}
	s.events.mu.Unlock()
}

// eventSettle ends an instance that has fallen below its Keep count.
func (s *Server) eventSettle(inst *eventInstance) {
	s.events.mu.Lock()
	keep := inst.definition.Keep
	if inst.cleared {
		keep = 1 // Victorious expedition members may finish picking up their drops.
	}
	short := !inst.starting && !inst.ended && len(inst.members) < keep
	s.events.mu.Unlock()
	if short {
		s.eventEnd(inst)
	}
}

// eventEnd releases the stage maps and sends everyone on them to the exit
// map. It never runs on the goroutine of a packet handler that still holds a
// character's scene, so the warps happen on their own goroutine.
func (s *Server) eventEnd(inst *eventInstance) {
	s.eventEndAt(inst, inst.definition.ExitMap)
}

func (s *Server) eventEndAt(inst *eventInstance, exitMap int) {
	s.events.mu.Lock()
	if inst.ended {
		s.events.mu.Unlock()
		return
	}
	s.closeEventMembersLocked(inst)
	s.events.mu.Unlock()
	occupants := s.eventOccupants(inst.channel, inst.definition.Maps)
	destination := inst.definition
	destination.ExitMap = exitMap
	go func() {
		for _, o := range occupants {
			s.eventExit(inst.channel, destination, o.live, o.sink)
		}
		if inst.definition.Name == "Papulatus" {
			s.setPapulatusGate(inst.channel, 0)
		}
		s.events.mu.Lock()
		key := eventKey{inst.channel, inst.definition.Name}
		if s.events.running[key] == inst {
			delete(s.events.running, key)
		}
		s.events.mu.Unlock()
	}()
}

// closeEventMembersLocked revokes entry while the event slot is still held.
func (s *Server) closeEventMembersLocked(inst *eventInstance) {
	inst.ended = true
	for id := range inst.members {
		if s.events.byMember[id] == inst {
			delete(s.events.byMember, id)
		}
	}
	clear(inst.members)
}

// eventExit sends one character from a stage map to the exit map.
func (s *Server) eventExit(channel int, e content.Event, live *liveCharacter, sink packetSink) {
	if live == nil || s.gameplay == nil {
		return
	}
	live.mu.Lock()
	mapID := live.session.Character.MapID
	live.mu.Unlock()
	if !e.Stage(mapID) {
		return
	}
	exit, err := s.gameplay.catalog.Map(e.ExitMap)
	if err != nil {
		return
	}
	portal, ok := exit.Spawn()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	guard := func(session domain.CharacterSession) bool {
		return session.ChannelID == channel && e.Stage(session.Character.MapID)
	}
	_ = s.warpGameplayConditional(ctx, channel, live, sink, exit.ID, portal, nil, guard, nil)
}

// eventEntered runs after a character has arrived on a map, at login as well
// as after a warp. A member on a stage map gets the clock; a member who left
// the stage maps is out of the group; anyone else found on a stage map is sent
// to the exit map unless they are a GM.
func (s *Server) eventEntered(channel int, live *liveCharacter, sink packetSink) {
	if s.events == nil || live == nil {
		return
	}
	s.oxQuizEntered(channel, live, sink)
	s.treasureEntered(channel, live, sink)
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	s.events.mu.Lock()
	inst := s.events.byMember[c.ID]
	var deadline time.Time
	if inst != nil {
		deadline = inst.deadline
	}
	s.events.mu.Unlock()
	if inst != nil {
		if inst.channel == channel && inst.definition.Stage(c.MapID) {
			if left := int(time.Until(deadline) / time.Second); left > 0 {
				_ = sink.WritePacket(protocol.Clock(left))
			}
			return
		}
		s.eventUnregister(inst, c.ID)
		s.eventSettle(inst)
		return
	}
	if e, ok := content.EventByMap(c.MapID); ok && (e.ExpeditionType != 0 || c.GM == 0) {
		go s.eventExit(channel, e, live, sink)
	}
}

// eventDeparted handles a member whose connection ended.
func (s *Server) eventDeparted(id int64) {
	if s.events == nil {
		return
	}
	s.events.mu.Lock()
	inst := s.events.byMember[id]
	s.events.mu.Unlock()
	if inst != nil {
		s.eventUnregister(inst, id)
		s.eventSettle(inst)
	}
}

// eventPartyChanged is called by every party mutation, under the party lock;
// the members who are gone leave their event on another goroutine.
func (s *Server) eventPartyChanged(before, after domain.PartySnapshot) {
	if s.events == nil {
		return
	}
	var gone []int64
	for _, m := range before.Members {
		if _, remains := after.Member(m.CharacterID); !remains {
			gone = append(gone, m.CharacterID)
		}
	}
	if len(gone) > 0 {
		go s.eventPartyLeft(before.ID, gone)
	}
}

// eventPartyLeft sends members who are no longer in the party to the exit
// map; arriving there takes them out of the group.
func (s *Server) eventPartyLeft(partyID int64, ids []int64) {
	if s.events == nil {
		return
	}
	for _, id := range ids {
		s.events.mu.Lock()
		inst := s.events.byMember[id]
		s.events.mu.Unlock()
		if inst == nil || inst.partyID != partyID {
			continue
		}
		moved := false
		for _, o := range s.eventOccupants(inst.channel, inst.definition.Maps) {
			if partySession(o.live).CharacterID == id {
				s.eventExit(inst.channel, inst.definition, o.live, o.sink)
				moved = true
			}
		}
		if !moved {
			s.eventUnregister(inst, id)
			s.eventSettle(inst)
		}
	}
}

// tickEvents ends instances whose time is up.
func (s *Server) tickEvents(now time.Time) {
	if s.events == nil {
		return
	}
	var due, preludes []*eventInstance
	s.events.mu.Lock()
	for _, inst := range s.events.running {
		if !inst.ended && inst.christmas != christmasRewarding && !now.Before(inst.deadline) {
			due = append(due, inst)
		} else if !inst.preludeUntil.IsZero() && !now.Before(inst.preludeUntil) {
			preludes = append(preludes, inst)
		}
	}
	s.events.mu.Unlock()
	for _, inst := range due {
		if !s.startChristmasBoss(inst, now) {
			s.eventEnd(inst)
		}
	}
	for _, inst := range preludes {
		s.startBalrogFight(inst, now)
	}
}

func (s *Server) eventOf(id int64) *eventInstance {
	if s.events == nil {
		return nil
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	return s.events.byMember[id]
}

// eventProperty reads a value the scripts of the running group stored; a
// character outside any group reads "".
func (s *Server) eventProperty(id int64, key string) string {
	inst := s.eventOf(id)
	if inst == nil {
		return ""
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	return inst.properties[key]
}

func (s *Server) setEventProperty(id int64, key, value string) error {
	if s.events == nil {
		return domain.ErrForbidden
	}
	s.events.mu.Lock()
	defer s.events.mu.Unlock()
	inst := s.events.byMember[id]
	if inst == nil {
		return domain.ErrForbidden
	}
	if _, exists := inst.properties[key]; !exists && len(inst.properties) >= eventPropertyLimit || len(value) > eventValueLimit {
		return domain.ErrInvalidInput
	}
	inst.properties[key] = value
	return nil
}

// resetEventRooms installs the stage maps for a new attempt. An in-flight kill
// must settle before a different group can reuse its map.
func (g *gameplaySpace) resetEventRooms(channel int, event content.Event) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range event.Maps {
		if room := g.rooms[mapKey{channel, id}]; room != nil {
			if len(room.members) != 0 {
				return domain.ErrAlreadyExists
			}
			for _, mob := range room.mobs {
				if mob.claiming || mob.reservation != nil {
					return domain.ErrAlreadyExists
				}
			}
		}
	}
	for _, id := range event.Maps {
		key := mapKey{channel, id}
		delete(g.rooms, key)
		room, err := g.room(key)
		if err != nil {
			return err
		}
		if id == content.PapulatusArena {
			for mobID := papulatusCrystal; mobID <= papulatusBody; mobID++ {
				if _, err := g.catalog.Mob(mobID); err != nil {
					return err
				}
			}
		}
		if id == content.MalaysiaBossArena {
			for _, statue := range content.MalaysiaBossStatues() {
				for mobID := statue.FirstForm; mobID <= statue.LastForm; mobID++ {
					if _, err := g.catalog.Mob(mobID); err != nil {
						return err
					}
				}
			}
		}
		if id == cygnusArena {
			if err := g.prepareCygnusBattle(room); err != nil {
				return err
			}
		}
		if id == pinkBeanArena {
			if err := g.preparePinkBeanBattle(room); err != nil {
				return err
			}
		}
		if stage, known := horntailByMap(id); known {
			if err := g.prepareHorntail(room, stage); err != nil {
				return err
			}
		}
		if required, checked := event.RequiredMobs[id]; checked {
			actual := make(map[int]int)
			for _, m := range room.mobs {
				if m.life.RespawnSeconds != -1 {
					return fmt.Errorf("event %s map %d contains a respawning monster", event.Name, id)
				}
				actual[m.life.TemplateID]++
			}
			if !maps.Equal(actual, required) {
				return fmt.Errorf("event %s map %d has unexpected WZ population: %v", event.Name, id, actual)
			}
		}
	}
	if event.Name == content.HenesysPQName {
		if err := g.prepareMoonRabbitLocked(g.rooms[mapKey{channel, content.HenesysPQMap}]); err != nil {
			return err
		}
	}
	for _, spawn := range event.Spawns {
		key := mapKey{channel, spawn.MapID}
		room := g.rooms[key]
		if spawn.Life.Kind == "n" {
			oid := g.alloc()
			if oid == 0 || room == nil {
				return errors.New("unable to spawn event NPC")
			}
			room.npcs[oid] = spawn.Life
			continue
		}
		stats, err := g.catalog.Mob(spawn.Life.TemplateID)
		if err != nil {
			return err
		}
		if christmas, ok := content.ChristmasByName(event.Name); ok && spawn.Life.TemplateID == christmas.Snowman {
			stats.Invincible = true
		}
		oid := g.alloc()
		if oid == 0 || room == nil {
			return errors.New("unable to spawn event monster")
		}
		room.mobs[oid] = &gameplayMob{objectID: oid, origin: spawn.Life, life: spawn.Life, stats: stats, hp: stats.HP,
			prelude: spawn.Prelude}
	}
	return nil
}
