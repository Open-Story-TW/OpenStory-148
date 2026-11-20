package world

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type orbisRoomProgress struct {
	defeats   int
	awarded   bool
	pixie     *gameplayMob
	pixieDead bool
}

var orbisStorageSpawns = [...]struct{ x, y int }{
	{200, -2321}, {-300, -2114}, {-300, -2910}, {-300, -2510}, {200, -1526}, {200, -2716}, {200, -717},
	{-300, -1310}, {-300, -3357}, {200, -1912}, {200, -1122}, {-300, -1736}, {-300, -915}, {200, -3116},
}

func (s *Server) orbisMobDefeated(key mapKey, actor int64, mob int) {
	storage := key.mapID == content.OrbisPQStorage && mob == 9300040
	garden := key.mapID == content.OrbisPQGarden && (mob == 9300049 || mob == 9300039)
	if !storage && !garden {
		return
	}
	inst := s.eventOf(actor)
	if inst == nil || inst.orbis == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[actor] == nil {
		g.mu.Unlock()
		return
	}
	progress := &room.orbisProgress
	var updates []roomDelivery
	switch {
	case storage:
		if progress.defeats < len(orbisStorageSpawns) {
			next := orbisStorageSpawns[progress.defeats]
			if red := g.orbisMob(room, 9300040, next.x, next.y); red != nil {
				updates = spawnOrbisMob(room, red)
			}
		}
		progress.defeats++
	case mob == 9300049 && progress.pixie == nil:
		if pixie := g.orbisMob(room, 9300039, -830, 563); pixie != nil {
			progress.pixie = pixie
			updates = roomBroadcast(room, protocol.PortalTrainingMessage("遠古精靈出現了。"), -1)
			updates = append(updates, spawnOrbisMob(room, pixie)...)
		}
	case mob == 9300039 && progress.pixie != nil && progress.pixie.hp == 0:
		progress.pixieDead = true
	}
	g.mu.Unlock()
	deliver(updates)
}

func (s *Server) orbisStage(ctx context.Context, live *liveCharacter, sink packetSink, snapshot domain.CharacterSession, npc int) (string, error) {
	inst := s.eventOf(snapshot.CharacterID)
	mapID := snapshot.Character.MapID
	if inst == nil || inst.orbis == nil || snapshot.Character.HP <= 0 || !inst.definition.Stage(mapID) {
		return "unavailable", nil
	}
	if npc == 2013002 && mapID == content.OrbisPQResult {
		return s.orbisResult(ctx, inst, live, sink)
	}
	if (npc != 2013001 && npc != 2013002) || s.partySnapshotOf(snapshot.CharacterID).LeaderID != snapshot.CharacterID {
		return "not_leader", nil
	}
	g := s.gameplay
	key := mapKey{channel: inst.channel, mapID: mapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[snapshot.CharacterID] == nil {
		g.mu.Unlock()
		return "unavailable", nil
	}
	if room.orbisProgress.awarded && mapID != content.OrbisPQHall {
		g.mu.Unlock()
		return "already", nil
	}
	if mapID == content.OrbisPQEntrance {
		seal := orbisEak(room)
		open := seal != nil && seal.state == orbisCloudCount+1
		g.mu.Unlock()
		if !open {
			return "clouds", nil
		}
		if err := s.awardEventStageEXP(ctx, inst, mapID, 6000); err != nil {
			return "unavailable", err
		}
		return s.orbisNPCTravel(ctx, inst, eventOccupant{live, sink}, mapID, content.OrbisPQHall)
	}
	if mapID == content.OrbisPQHall {
		statue := orbisReactor(room, orbisStatue)
		if statue == nil {
			g.mu.Unlock()
			return "unavailable", nil
		}
		step := statue.state
		g.mu.Unlock()
		switch {
		case npc == 2013001 && step == len(orbisStatueSockets) && henesysItemCount(snapshot.Character, 4001055) > 0:
			return "restore", nil
		case npc == 2013001 && step == len(orbisStatueSockets):
			return s.orbisNPCTravel(ctx, inst, eventOccupant{live, sink}, mapID, content.OrbisPQGarden)
		case npc == 2013002 && step == len(orbisStatueSockets)+1:
			return s.orbisBonus(ctx, inst, eventOccupant{live, sink})
		case step > len(orbisStatueSockets):
			return "minerva", nil
		default:
			return "statue", nil
		}
	}
	if npc != 2013001 {
		g.mu.Unlock()
		return "unavailable", nil
	}
	var payment, quantity, prize int
	status := "claimed"
	switch mapID {
	case content.OrbisPQWalkway:
		payment, quantity, prize = 4001050, 30, 4001048
	case content.OrbisPQStorage:
		payment, quantity, prize = 4001051, 15, 4001045
	case content.OrbisPQMusic:
		if player := orbisReactor(room, orbisPlayer); player == nil || player.state == 0 {
			status = "music"
		}
		prize = 4001046
	case content.OrbisPQAscent:
		leverStates := [3]bool{}
		for _, reactor := range room.reactors {
			if reactor.def.TemplateID != orbisLever {
				continue
			}
			index := 1
			if reactor.def.X < -130 {
				index = 0
			} else if reactor.def.X > 70 {
				index = 2
			}
			leverStates[index] = reactor.state == 1
		}
		for index, on := range leverStates {
			if on != (index != inst.orbis.restingLever) {
				status = "levers"
			}
		}
		prize = 4001049
	case content.OrbisPQGarden:
		if !room.orbisProgress.pixieDead {
			status = "garden"
		}
		payment, quantity, prize = 4001054, 1, 4001055
	default:
		status = "unavailable"
	}
	g.mu.Unlock()
	if status != "claimed" {
		return status, nil
	}
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, fmt.Sprintf("orbis-%s-room-%d", inst.id, mapID), func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || current.ChannelID != inst.channel || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		items := []domain.ScriptItemDelta{{TemplateID: prize, Quantity: 1}}
		if payment != 0 {
			if henesysItemCount(current.Character, payment) < quantity {
				return domain.ScriptExchange{}, domain.ErrInsufficient
			}
			items = append(items, domain.ScriptItemDelta{TemplateID: payment, Quantity: -quantity})
		}
		return domain.ScriptExchange{Items: items}, nil
	})
	if errors.Is(err, domain.ErrInsufficient) {
		return "items", nil
	}
	if err != nil {
		return "unavailable", err
	}
	g.mu.Lock()
	room.orbisProgress.awarded = true
	g.mu.Unlock()
	effect := eventEffects["clear"]
	s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldScreenEffect(effect[0]))
	s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldSoundEffect(effect[1]))
	if mapID != content.OrbisPQGarden {
		_ = s.awardEventStageEXP(ctx, inst, mapID, 3500)
	}
	return "claimed", nil
}

func (s *Server) orbisNPCTravel(ctx context.Context, inst *eventInstance, actor eventOccupant, from, to int) (string, error) {
	m, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return "unavailable", err
	}
	entry, ok := m.Spawn()
	if !ok {
		return "unavailable", nil
	}
	for _, member := range s.eventOccupants(inst.channel, []int{from}) {
		if member.live == actor.live || s.eventOf(partySession(member.live).CharacterID) != inst {
			continue
		}
		s.cancelNPCScriptState(member.live, false)
		s.closeEconomyDialog(member.live)
		if err := s.warpEventMember(ctx, inst, from, member, to, entry); err != nil {
			return "unavailable", err
		}
	}
	if err := s.warpEventMember(ctx, inst, from, actor, to, entry); err != nil {
		return "unavailable", err
	}
	return "moved", nil
}

func (s *Server) orbisBonus(ctx context.Context, inst *eventInstance, actor eventOccupant) (string, error) {
	s.events.mu.Lock()
	if inst.ended || inst.orbis.bonusStarted {
		s.events.mu.Unlock()
		return "unavailable", nil
	}
	inst.orbis.bonusStarted = true
	inst.cleared = true
	inst.deadline = time.Now().Add(6 * time.Minute)
	s.events.mu.Unlock()
	status, err := s.orbisNPCTravel(ctx, inst, actor, content.OrbisPQHall, content.OrbisPQBonus)
	if err != nil || status != "moved" {
		s.events.mu.Lock()
		inst.orbis.bonusStarted = false
		s.events.mu.Unlock()
		return status, err
	}
	for _, m := range s.eventOccupants(inst.channel, []int{content.OrbisPQBonus}) {
		if s.eventOf(partySession(m.live).CharacterID) == inst {
			_ = m.sink.WritePacket(protocol.Clock(60))
		}
	}
	time.AfterFunc(time.Minute, func() {
		m, err := s.gameplay.catalog.Map(content.OrbisPQResult)
		if err != nil {
			return
		}
		entry, ok := m.Spawn()
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		for _, member := range s.eventOccupants(inst.channel, []int{content.OrbisPQBonus}) {
			if s.eventOf(partySession(member.live).CharacterID) == inst {
				_ = s.warpEventMember(ctx, inst, content.OrbisPQBonus, member, m.ID, entry)
			}
		}
	})
	return status, nil
}

func (s *Server) orbisResult(ctx context.Context, inst *eventInstance, live *liveCharacter, sink packetSink) (string, error) {
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, "orbis-clear-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != content.OrbisPQResult || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		level := current.Character.Level
		scaledLevel := level
		if level > 120 {
			scaledLevel = 120 + level/10
		}
		base := content.ExperienceForLevel(scaledLevel)
		divisor := int64(max(1, min(level, 120)))
		return domain.ScriptExchange{
			EXP:   (10*base + divisor/2) / divisor,
			Items: []domain.ScriptItemDelta{{TemplateID: 4001158, Quantity: 1}},
			Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "orbis-pq-clear"}, Delta: 1}},
		}, nil
	})
	if err != nil {
		return "unavailable", err
	}
	return "reward", nil
}
