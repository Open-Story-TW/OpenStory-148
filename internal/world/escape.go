package world

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type escapeState struct {
	mu      sync.Mutex
	maze    [6][4]int
	reached map[int]bool
	rewards map[int64]domain.ScriptExchange
}

func newEscapeState() *escapeState {
	state := &escapeState{reached: make(map[int]bool), rewards: make(map[int64]domain.ScriptExchange)}
	// Each light has a stable destination for the attempt. Tower 5 alone
	// leads to Tower 6. Every other wrong turn can return to Tower 1.
	for room := range state.maze {
		var choices []int
		for target := 0; target < 5; target++ {
			if target != room {
				choices = append(choices, content.EscapeMazeFirst+10*target)
			}
		}
		if room == 4 {
			choices[3] = content.EscapeMazeLast
		} else if room == 5 {
			choices = choices[:4]
		}
		rand.Shuffle(len(choices), func(i, j int) { choices[i], choices[j] = choices[j], choices[i] })
		copy(state.maze[room][:], choices)
	}
	return state
}

func (g *gameplaySpace) validateEscapePrison(room *gameplayRoom) error {
	reactors := map[int]int{}
	for _, r := range room.reactors {
		reactors[r.def.TemplateID]++
	}
	if reactors[content.EscapePrisonDoor] != 4 || reactors[content.EscapeCommonChest] != 3 ||
		reactors[content.EscapeKeyChest] != 1 || reactors[content.EscapeFinalGate] != 1 {
		return fmt.Errorf("Escape has an incomplete prison reactor layout: %v", reactors)
	}
	mobs := map[int]int{}
	for _, m := range room.mobs {
		mobs[m.stats.ID]++
	}
	if len(mobs) != 2 || mobs[9300452] != 4 || mobs[9300453] != 4 {
		return fmt.Errorf("Escape has an incomplete prison guard roster: %v", mobs)
	}
	return nil
}

func (s *Server) startEscape(ctx context.Context, inst *eventInstance, members []eventOccupant) error {
	drops, ok := s.repo.(domain.EventDropRepository)
	if !ok {
		return domain.ErrForbidden
	}
	if err := drops.ExpireEventDrops(ctx, s.cfg.WorldID, inst.channel, content.EscapePrison); err != nil {
		return err
	}
	for _, member := range members {
		err := s.applyEventExchange(ctx, member, "prison-entry-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if session.Character.MapID != content.EscapeLobby || s.eventOf(session.CharacterID) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			n := henesysItemCount(session.Character, content.EscapeKey)
			if n == 0 {
				return domain.ScriptExchange{}, nil
			}
			return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: content.EscapeKey, Quantity: -n}}}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) escapeStagePortal(ctx context.Context, snapshot domain.CharacterSession, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	switch portal.Script {
	case "prisonBreak_next", "prisonBreak_miro", "prison_1stage", "prison_5stage":
	default:
		return false, nil
	}
	mapID := snapshot.Character.MapID
	inst := s.eventOf(snapshot.CharacterID)
	if inst == nil || inst.escape == nil || inst.channel != snapshot.ChannelID ||
		!inst.definition.Stage(mapID) || !time.Now().Before(inst.deadline) {
		return true, eventPortalNotice(sink, "這道門目前無法通行。")
	}
	state := inst.escape
	state.mu.Lock()
	defer state.mu.Unlock()

	if content.EscapeMazeMap(mapID) && portal.Script == "prisonBreak_miro" && portal.ID >= 2 && portal.ID <= 5 {
		next := state.maze[(mapID-content.EscapeMazeFirst)/10][portal.ID-2]
		if next == 0 {
			return true, eventPortalNotice(sink, "這道門目前無法通行。")
		}
		entry, err := s.escapeEntry(next)
		if err != nil {
			return true, err
		}
		s.cancelNPCScriptState(live, false)
		s.closeEconomyDialog(live)
		err = s.warpEventMember(ctx, inst, mapID, eventOccupant{live, sink}, next, entry)
		if err == nil && next == content.EscapeMazeLast {
			_ = sink.WritePacket(protocol.PortalTrainingMessage("找到通往空中監獄之門。"))
		}
		return true, err
	}

	var next int
	switch mapID {
	case content.EscapeTower, content.EscapeTraps:
		if portal.Name == "goal00" {
			if !state.reached[mapID] {
				state.reached[mapID] = true
				s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldScreenEffect(eventEffects["clear"][0]))
				s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldSoundEffect(eventEffects["clear"][1]))
			}
			return true, sink.WritePacket(protocol.EnableActions())
		}
		// The visible exit is at the top as well. The native clear marker is
		// separately triggered and is not an extra admission requirement.
		if portal.Name == "next00" {
			next = mapID + 100
		}
	case content.EscapeApproach, content.EscapePassage:
		if portal.Name == "next00" && s.escapeMobsDefeated(inst.channel, mapID) {
			next = mapID + 100
		}
	case content.EscapeMazeLast:
		if portal.Name == "next00" {
			next = content.EscapePassage
		}
	case content.EscapePrison:
		if portal.Name == "next00" && s.escapeDoorsOpened(inst.channel) {
			next = content.EscapeBossRoom
		}
	}
	if next == 0 {
		return true, eventPortalNotice(sink, "請先完成這一關。")
	}
	entry, err := s.escapeEntry(next)
	if err != nil {
		return true, err
	}
	if mapID == content.EscapeMazeLast {
		for field := content.EscapeMazeFirst; field < content.EscapeMazeLast; field += 10 {
			s.warpEventPeers(ctx, inst, field, live, next, entry)
		}
	}
	return true, s.moveEventRoom(ctx, inst, mapID, eventOccupant{live, sink}, next, entry)
}

func (s *Server) escapeEntry(mapID int) (content.Portal, error) {
	mapData, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return content.Portal{}, err
	}
	entry, ok := mapData.Spawn()
	if !ok {
		return content.Portal{}, domain.ErrInvalidInput
	}
	return entry, nil
}

func (s *Server) escapeMobsDefeated(channel, mapID int) bool {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{channel, mapID}]
	if room == nil {
		return false
	}
	for _, mob := range room.mobs {
		if mob.hp > 0 || mob.claiming || mob.reservation != nil {
			return false
		}
	}
	return true
}

func (s *Server) escapeDoorsOpened(channel int) bool {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{channel, content.EscapePrison}]
	if room == nil {
		return false
	}
	return escapeDoorsOpenedLocked(room)
}

func escapeDoorsOpenedLocked(room *gameplayRoom) bool {
	opened := 0
	for _, r := range room.reactors {
		if r.def.TemplateID == content.EscapePrisonDoor && r.state == 1 {
			opened++
		}
	}
	return opened == 4
}

func (s *Server) escapeClick(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) error {
	snapshot := partySession(live)
	inst := s.eventOf(snapshot.CharacterID)
	if snapshot.Character.MapID != content.EscapePrison || inst == nil || inst.escape == nil || inst.channel != channel ||
		snapshot.Character.HP <= 0 || !time.Now().Before(inst.deadline) || s.tradeActive(live) {
		return nil
	}
	inst.escape.mu.Lock()
	defer inst.escape.mu.Unlock()
	visit := s.sceneFor(live, sink)
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, content.EscapePrison}]
	if room == nil || visit == nil || room.members[snapshot.CharacterID] == nil || room.members[snapshot.CharacterID].visit != visit {
		g.mu.Unlock()
		return nil
	}
	reactor := room.reactors[oid]
	if reactor == nil || reactor.gone || reactor.state != 0 || reactor.def.TemplateID == content.EscapeFinalGate ||
		!within(snapshot.Character.X, snapshot.Character.Y, reactor.def.X, reactor.def.Y, 130) {
		g.mu.Unlock()
		return nil
	}
	def := reactor.def
	g.mu.Unlock()
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, fmt.Sprintf("prison-reactor-%s-%d", inst.id, oid),
		func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if session.Character.MapID != content.EscapePrison || s.eventOf(session.CharacterID) != inst || !s.sceneCurrent(visit) ||
				!time.Now().Before(inst.deadline) || !within(session.Character.X, session.Character.Y, def.X, def.Y, 130) {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			switch def.TemplateID {
			case content.EscapePrisonDoor:
				return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: content.EscapeKey, Quantity: -1}}}, nil
			case content.EscapeKeyChest:
				x, y := s.gatherDropPoint(session.Character.MapID, def.X, def.Y, 25)
				return domain.ScriptExchange{Drops: []domain.ScriptDrop{{TemplateID: content.EscapeKey, Quantity: 1, X: x, Y: y}}}, nil
			}
			return domain.ScriptExchange{}, nil
		})
	if errors.Is(err, domain.ErrInsufficient) {
		return eventPortalNotice(sink, "需要一把監獄鑰匙才能開門。")
	}
	if err != nil {
		return err
	}
	g.mu.Lock()
	reactor.state = 1
	packets := roomBroadcast(room, protocol.ReactorState(oid, reactor.state, def.X, def.Y), -1)
	if def.TemplateID == content.EscapePrisonDoor && escapeDoorsOpenedLocked(room) {
		for _, gate := range room.reactors {
			if gate.def.TemplateID == content.EscapeFinalGate {
				gate.state = 1
				packets = append(packets, roomBroadcast(room, protocol.ReactorState(gate.objectID, 1, gate.def.X, gate.def.Y), -1)...)
			}
		}
		packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage("四扇監獄門都已開啟。"), -1)...)
	}
	g.mu.Unlock()
	deliver(packets)
	return sink.WritePacket(protocol.EnableActions())
}

func (a *worldScript) finishEscape(ctx context.Context, snapshot domain.CharacterSession) (string, error) {
	s := a.server
	inst := s.eventOf(snapshot.CharacterID)
	if a.npcID != content.EscapeCaptiveNPC || snapshot.Character.MapID != content.EscapeBossRoom ||
		inst == nil || inst.escape == nil || !time.Now().Before(inst.deadline) || snapshot.Character.HP <= 0 {
		return "unavailable", nil
	}
	if !s.escapeMobsDefeated(inst.channel, content.EscapeBossRoom) {
		return "boss", nil
	}
	inst.escape.mu.Lock()
	defer inst.escape.mu.Unlock()
	err := s.applyEventExchange(ctx, eventOccupant{a.live, a.sink}, "prison-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
		if session.Character.MapID != content.EscapeBossRoom || s.eventOf(session.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		if reward, ok := inst.escape.rewards[session.CharacterID]; ok {
			return reward, nil
		}
		level := session.Character.Level
		scaled := level
		if level > 200 {
			scaled = 200 + level/10
		}
		// Legacy gainExp_PQ(200, 1.5).
		denominator := int64(min(level, 200) * 3)
		var items []domain.ScriptItemDelta
		if count := henesysItemCount(session.Character, content.EscapeKey); count > 0 {
			items = append(items, domain.ScriptItemDelta{TemplateID: content.EscapeKey, Quantity: -count})
		}
		items = append(items, domain.ScriptItemDelta{TemplateID: content.EscapeRewardKey, Quantity: 1})
		reward := domain.ScriptExchange{Items: items, EXP: (content.ExperienceForLevel(scaled)*20 + denominator/2) / denominator,
			Logs: []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "prison-clears"}, Delta: 1}}}
		inst.escape.rewards[session.CharacterID] = reward
		return reward, nil
	})
	if errors.Is(err, domain.ErrInsufficient) {
		delete(inst.escape.rewards, snapshot.CharacterID)
		return "inventory", nil
	}
	if err != nil {
		return "unavailable", err
	}
	return "completed", nil
}
