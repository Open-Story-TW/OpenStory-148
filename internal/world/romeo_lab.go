package world

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type romeoLabSearch struct {
	switchPortal string
	letterPortal string
	searched     map[string]bool
	opened       bool
}

func (s *Server) romeoLabPortal(ctx context.Context, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	switch portal.Script {
	case "party5_click", "rnj1_pt00", "jnr1_pt00", "rnj1_out", "jnr1_out":
	default:
		return false, nil
	}
	session := partySession(live)
	inst := s.eventOf(session.CharacterID)
	if inst == nil || (inst.definition.Name != content.RomeoPQName && inst.definition.Name != content.JulietPQName) {
		return false, nil
	}
	first, current := inst.definition.Maps[0], session.Character.MapID
	actor := eventOccupant{live, sink}
	var next int
	switch {
	case current == first && portal.Script == "party5_click" && portal.Type == 11:
		return true, s.romeoSearchBooks(ctx, inst, actor, portal.Name)
	case current == first && (portal.Script == "rnj1_pt00" || portal.Script == "jnr1_pt00"):
		s.events.mu.Lock()
		opened := !inst.ended && inst.romeoSearch != nil && inst.romeoSearch.opened
		s.events.mu.Unlock()
		if !opened {
			return true, sink.WritePacket(protocol.PortalTrainingMessage("請先找出研究室裡隱藏的開關。"))
		}
		next = first + 1
	case current == first+1 && (portal.Script == "rnj1_out" || portal.Script == "jnr1_out"):
		s.gameplay.mu.Lock()
		room := s.gameplay.rooms[mapKey{inst.channel, current}]
		uncleared := room == nil
		if room != nil {
			for _, mob := range room.mobs {
				if mob.hp > 0 || mob.claiming || mob.reservation != nil {
					uncleared = true
					break
				}
			}
		}
		s.gameplay.mu.Unlock()
		if uncleared {
			return true, sink.WritePacket(protocol.PortalTrainingMessage("研究室內還有怪物。"))
		}
		next = first + 100
	default:
		return false, nil
	}
	target, err := s.gameplay.catalog.Map(next)
	if err != nil {
		return true, err
	}
	entry, ok := target.Spawn()
	if !ok {
		return true, domain.ErrNotFound
	}
	return true, s.warpEventMember(ctx, inst, current, actor, next, entry)
}

func (s *Server) romeoSearchBooks(ctx context.Context, inst *eventInstance, actor eventOccupant, name string) error {
	s.events.mu.Lock()
	if inst.ended {
		s.events.mu.Unlock()
		return actor.sink.WritePacket(protocol.EnableActions())
	}
	if inst.romeoSearch == nil {
		switchIndex := 1 + rand.IntN(26)
		letterIndex := 1 + rand.IntN(25)
		if letterIndex >= switchIndex {
			letterIndex++
		}
		inst.romeoSearch = &romeoLabSearch{
			switchPortal: fmt.Sprintf("click%02d", switchIndex),
			letterPortal: fmt.Sprintf("click%02d", letterIndex),
			searched:     make(map[string]bool),
		}
	}
	search := inst.romeoSearch
	if search.searched[name] {
		s.events.mu.Unlock()
		return actor.sink.WritePacket(protocol.PortalTrainingMessage("這裡已經調查過了。"))
	}
	search.searched[name] = true
	if name == search.switchPortal {
		search.opened = true
	}
	s.events.mu.Unlock()
	if name == search.switchPortal {
		s.openRomeoLabDoor(inst)
		return actor.sink.WritePacket(protocol.PortalTrainingMessage("找到開關，研究室的門已打開。"))
	}
	if name != search.letterPortal {
		return actor.sink.WritePacket(protocol.PortalTrainingMessage("這裡沒有發現可疑的東西。"))
	}
	letter := 4001130
	if inst.definition.Name == content.JulietPQName {
		letter = 4001131
	}
	err := s.applyEventExchange(ctx, actor, "romeo-letter-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if s.eventOf(current.CharacterID) != inst || current.Character.MapID != inst.definition.Maps[0] {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: letter, Quantity: 1}}}, nil
	})
	if err != nil {
		s.events.mu.Lock()
		delete(search.searched, name)
		s.events.mu.Unlock()
		if errors.Is(err, domain.ErrInsufficient) {
			return actor.sink.WritePacket(protocol.PortalTrainingMessage("請先保留一格其他欄位。"))
		}
		return err
	}
	return actor.sink.WritePacket(protocol.PortalTrainingMessage("發現了一封和研究室有關的信。"))
}

func (s *Server) openRomeoLabDoor(inst *eventInstance) {
	game := s.gameplay
	game.mu.Lock()
	var updates []roomDelivery
	if room := game.rooms[mapKey{inst.channel, inst.definition.Maps[0]}]; room != nil {
		for id, reactor := range room.reactors {
			if reactor.def.TemplateID == 2618005 {
				reactor.state = 1
				updates = roomBroadcast(room, protocol.ReactorState(id, 1, reactor.def.X, reactor.def.Y), -1)
				break
			}
		}
	}
	game.mu.Unlock()
	deliver(updates)
}
