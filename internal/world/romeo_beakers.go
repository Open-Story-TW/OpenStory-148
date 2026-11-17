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

const (
	romeoBeaker      = content.RomeoLabBeaker
	romeoBeakerDoor  = 2618006
	romeoLiquid      = content.RomeoLabLiquid
	romeoBeakerFull  = 7
	romeoBeakerDrain = 9500 * time.Millisecond // v148 Reactor.wz/2618000, states 1–6
)

func romeoFullBeakers(room *gameplayRoom) int {
	if room == nil {
		return 0
	}
	count := 0
	for _, reactor := range room.reactors {
		if reactor.def.TemplateID == romeoBeaker && reactor.state == romeoBeakerFull {
			count++
		}
	}
	return count
}

func (r *roomReactor) drainRomeoBeaker(now time.Time) bool {
	if r.drainsAt.IsZero() || r.finishing || now.Before(r.drainsAt) {
		return false
	}
	r.state--
	r.drainsAt = time.Time{}
	if r.state > 0 {
		r.drainsAt = now.Add(romeoBeakerDrain)
	}
	return true
}

func (s *Server) clickRomeoBeaker(ctx context.Context, live *liveCharacter, sink packetSink, oid int32) error {
	session := partySession(live)
	mapID := session.Character.MapID
	if session.Character.HP <= 0 || !content.RomeoLabReactor(mapID, romeoBeaker) {
		return nil
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || !inst.definition.Stage(mapID) {
		return nil
	}
	key := mapKey{inst.channel, mapID}
	game := s.gameplay
	game.mu.Lock()
	room := game.rooms[key]
	if room == nil || room.members[session.CharacterID] == nil {
		game.mu.Unlock()
		return nil
	}
	beaker := room.reactors[oid]
	if beaker == nil || beaker.def.TemplateID != romeoBeaker || beaker.finishing || beaker.state >= romeoBeakerFull ||
		!within(session.Character.X, session.Character.Y, beaker.def.X, beaker.def.Y, 120) {
		game.mu.Unlock()
		return nil
	}
	beaker.finishing = true
	beaker.growth++
	op := fmt.Sprintf("rnj-pour-%s-%d-%d", inst.id, oid, beaker.growth)
	game.mu.Unlock()

	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, op, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: romeoLiquid, Quantity: -1}}}, nil
	})
	game.mu.Lock()
	beaker.finishing = false
	if err != nil || game.rooms[key] != room || room.reactors[oid] != beaker {
		game.mu.Unlock()
		if errors.Is(err, domain.ErrInsufficient) {
			return sink.WritePacket(protocol.PortalTrainingMessage("需要一罐可疑液體。"))
		}
		return err
	}
	beaker.state++
	if beaker.state == romeoBeakerFull {
		beaker.drainsAt = time.Time{}
	} else {
		beaker.drainsAt = time.Now().Add(romeoBeakerDrain)
	}
	updates := roomBroadcast(room, protocol.ReactorState(oid, beaker.state, beaker.def.X, beaker.def.Y), -1)
	if beaker.state == romeoBeakerFull {
		count := romeoFullBeakers(room)
		for id, reactor := range room.reactors {
			if reactor.def.TemplateID == romeoBeakerDoor {
				reactor.state = count
				updates = append(updates, roomBroadcast(room, protocol.ReactorState(id, count, reactor.def.X, reactor.def.Y), -1)...)
				break
			}
		}
		if count == 3 {
			clear := eventEffects["clear"]
			updates = append(updates, roomBroadcast(room, protocol.FieldScreenEffect(clear[0]), -1)...)
			updates = append(updates, roomBroadcast(room, protocol.FieldSoundEffect(clear[1]), -1)...)
		}
	}
	game.mu.Unlock()
	deliver(updates)
	return nil
}
