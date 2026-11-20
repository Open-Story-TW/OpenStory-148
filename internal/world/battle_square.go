package world

import (
	"context"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

const (
	battleSquareHub   = 960000000
	battleSquareEvent = "BattleSquareSurvival"
)

// battleSquareMatch lives under eventSpace.mu. Members must finish their
// field transition before they are counted toward the start threshold.
type battleSquareMatch struct {
	rank       uint8
	minimum    int
	roomReady  bool
	started    bool
	lastAttack map[int64]time.Time
	arrived    map[int64]bool
}

func battleSquareRankEligible(rank uint8, level int) bool {
	switch rank {
	case 0:
		return level >= 30 && level < 70
	case 1:
		return level >= 70
	case 2:
		return level >= 120
	case 3:
		return level >= 180
	}
	return false
}

func (s *Server) handleBattleSquareList(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	if r.Remaining() != 0 {
		return nil
	}
	session := partySession(live)
	if session.Character.MapID != battleSquareHub || !s.sceneCurrent(s.sceneFor(live, sink)) {
		return nil
	}
	count := 0
	if s.events != nil {
		s.events.mu.Lock()
		if inst := s.events.running[eventKey{session.ChannelID, battleSquareEvent}]; inst != nil && !inst.ended {
			count = len(inst.members)
		}
		s.events.mu.Unlock()
	}
	return sink.WritePacket(protocol.BattleSquareList(count))
}

func (s *Server) enterBattleSquare(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader, party bool) error {
	q, err := protocol.ReadBattleSquareEntry(r)
	if err != nil {
		return nil
	}
	blocked := func(code byte) error {
		if err := sink.WritePacket(protocol.BattleSquareBlocked(code)); err != nil {
			return err
		}
		return sink.WritePacket(protocol.EnableActions())
	}
	if s.events == nil || s.gameplay == nil || q.Index != 0 || q.Mode != 0 || q.Option != 0 {
		return blocked(1)
	}
	session := partySession(live)
	if session.Character.MapID != battleSquareHub || session.Character.HP <= 0 || !battleSquareRankEligible(q.Rank, session.Character.Level) ||
		!s.sceneCurrent(s.sceneFor(live, sink)) || s.tradeActive(live) {
		return blocked(1)
	}
	if party || s.partySnapshotOf(session.CharacterID).ID != 0 {
		return blocked(9)
	}
	e, ok := content.EventByName(battleSquareEvent)
	if !ok {
		return blocked(1)
	}
	dest, err := s.gameplay.catalog.Map(e.Maps[0])
	if err != nil {
		return blocked(1)
	}
	entry, ok := dest.Spawn()
	if !ok {
		return blocked(1)
	}
	key := eventKey{session.ChannelID, e.Name}
	id := session.CharacterID
	occupied := len(s.eventOccupants(session.ChannelID, e.Maps)) != 0
	minimum := e.PartyMin
	if session.Character.GM > 0 {
		minimum = 2 // Legacy Survival starts at two players when a GM joins.
	}
	s.events.mu.Lock()
	inst := s.events.running[key]
	fresh := inst == nil
	if s.events.byMember[id] != nil || fresh && occupied || !fresh && (inst.ended || !inst.battleSquare.roomReady || inst.battleSquare.started || inst.battleSquare.rank != q.Rank || len(inst.members) >= e.PartyMax) {
		s.events.mu.Unlock()
		return blocked(1)
	}
	if fresh {
		inst = &eventInstance{id: platform.NewID(), definition: e, channel: session.ChannelID,
			members: map[int64]struct{}{}, properties: map[string]string{}, deadline: time.Now().Add(time.Duration(e.Seconds) * time.Second),
			battleSquare: &battleSquareMatch{rank: q.Rank, minimum: minimum, arrived: map[int64]bool{}, lastAttack: map[int64]time.Time{}}}
		s.events.running[key] = inst
	} else if minimum < inst.battleSquare.minimum {
		inst.battleSquare.minimum = minimum
	}
	inst.members[id] = struct{}{}
	s.events.byMember[id] = inst
	s.events.mu.Unlock()
	if fresh {
		if err := s.gameplay.resetEventRooms(inst.channel, e); err != nil {
			s.eventEnd(inst)
			return blocked(1)
		}
		s.events.mu.Lock()
		inst.battleSquare.roomReady = true
		s.events.mu.Unlock()
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	if err := s.warpEventMember(ctx, inst, battleSquareHub, eventOccupant{live, sink}, dest.ID, entry); err != nil {
		s.eventUnregister(inst, id)
		s.eventSettle(inst)
		return blocked(1)
	}
	return nil
}

func (s *Server) battleSquareEntered(inst *eventInstance, id int64, sink packetSink) {
	s.events.mu.Lock()
	if inst.ended || s.events.byMember[id] != inst {
		s.events.mu.Unlock()
		return
	}
	match := inst.battleSquare
	match.arrived[id] = true
	start := !match.started && len(match.arrived) >= match.minimum
	if start {
		match.started = true
		inst.deadline = time.Now().Add(time.Duration(inst.definition.Seconds) * time.Second)
	}
	rank := match.rank
	s.events.mu.Unlock()
	if !start {
		_ = sink.WritePacket(protocol.BattleSquareSurvivalState(rank, false))
		return
	}
	for _, o := range s.eventOccupants(inst.channel, inst.definition.Maps) {
		if s.eventOf(partySession(o.live).CharacterID) != inst {
			continue
		}
		_ = o.sink.WritePacket(protocol.BattleSquareSurvivalState(rank, true))
		_ = o.sink.WritePacket(protocol.BattleSquareScore(0))
		_ = o.sink.WritePacket(protocol.BattleSquareEnable())
		_ = o.sink.WritePacket(protocol.Clock(inst.definition.Seconds))
	}
}

func (s *Server) leaveBattleSquare(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	_ = r.U32()
	if r.Err() != nil || r.Remaining() != 0 {
		return nil
	}
	session := partySession(live)
	inst := s.eventOf(session.CharacterID)
	if inst != nil && inst.battleSquare != nil && inst.definition.Stage(session.Character.MapID) && s.sceneCurrent(s.sceneFor(live, sink)) {
		s.eventExit(inst.channel, inst.definition, live, sink)
	}
	return nil
}
