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

func (s *Server) romeoSearchNPC(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	session := partySession(live)
	mapID := session.Character.MapID
	if !content.RomeoSearchNPC(mapID, 2112007) && !content.RomeoSearchNPC(mapID, 2112013) {
		return false, nil
	}
	g := s.gameplay
	g.mu.Lock()
	var npc content.Life
	if room := g.rooms[mapKey{channel, mapID}]; room != nil {
		npc = room.npcs[oid]
	}
	g.mu.Unlock()
	if !content.RomeoSearchNPC(mapID, npc.TemplateID) {
		return false, nil
	}
	if session.Character.HP <= 0 || !within(session.Character.X, session.Character.Y, npc.X, npc.Y, 150) || s.tradeActive(live) {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	field, err := g.catalog.Map(mapID)
	if err != nil {
		return true, err
	}
	for _, p := range field.Portals {
		if p.Type == 11 && p.Script == "party5_click" && p.X == npc.X && abs(p.Y-npc.Y) <= 12 {
			inst := s.eventOf(session.CharacterID)
			if inst != nil && mapID == inst.definition.Maps[0] {
				return true, s.romeoSearchBooks(ctx, inst, eventOccupant{live, sink}, p.Name, npc.TemplateID)
			}
			return s.romeoLabPortal(ctx, live, sink, p)
		}
	}
	return true, sink.WritePacket(protocol.EnableActions())
}

func (s *Server) romeoLabPortal(ctx context.Context, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	session := partySession(live)
	if session.Character.MapID < 926100000 || session.Character.MapID > 926110600 {
		return false, nil
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || !content.RomeoJulietEvent(inst.definition.Name) {
		return false, nil
	}
	first, current := inst.definition.Maps[0], session.Character.MapID
	actor := eventOccupant{live, sink}
	if current >= first+200 && current <= first+202 {
		return s.romeoArchivePortal(ctx, inst, actor, current, portal)
	}
	if current == first+203 {
		return s.romeoMadmanPortal(ctx, inst, actor, session, portal)
	}
	if current >= first+300 && current <= first+400 {
		return s.romeoLanePortal(ctx, inst, actor, session, portal)
	}
	var next int
	switch {
	case current == first && portal.Script == "party5_click" && portal.Type == 11:
		return true, s.romeoSearchBooks(ctx, inst, actor, portal.Name, 0)
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
	case current == first+100 && (portal.Script == "rnj2_out" || portal.Script == "jnr2_out"):
		s.gameplay.mu.Lock()
		room := s.gameplay.rooms[mapKey{inst.channel, current}]
		filled := romeoFullBeakers(room) == 3
		s.gameplay.mu.Unlock()
		if !filled {
			return true, sink.WritePacket(protocol.PortalTrainingMessage("請將三個燒杯都裝滿可疑液體。"))
		}
		next = first + 200
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

func (s *Server) clearRomeoSupplies(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	if repo, ok := s.repo.(domain.EventDropRepository); ok {
		for _, mapID := range inst.definition.Maps {
			if err := repo.ExpireEventDrops(ctx, s.cfg.WorldID, inst.channel, mapID); err != nil {
				return err
			}
		}
	}
	for _, actor := range group {
		err := s.applyEventExchange(ctx, actor, "romeo-supplies-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
			if current.Character.MapID != inst.definition.LobbyMap || s.eventOf(current.CharacterID) != inst || s.tradeActive(actor.live) {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{Items: romeoSuppliesToRemove(current.Character)}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func romeoSuppliesToRemove(c domain.Character) []domain.ScriptItemDelta {
	var quantities [6]int
	for _, item := range c.Items {
		if item.TemplateID >= 4001130 && item.TemplateID <= 4001135 {
			quantities[item.TemplateID-4001130] += item.Quantity
		}
	}
	var items []domain.ScriptItemDelta
	for i, count := range quantities {
		if count > 0 {
			items = append(items, domain.ScriptItemDelta{TemplateID: 4001130 + i, Quantity: -count})
		}
	}
	return items
}

func (s *Server) romeoSearchBooks(ctx context.Context, inst *eventInstance, actor eventOccupant, name string, npcID int) error {
	notice := func(text string) error {
		if npcID != 0 {
			return actor.sink.WritePacket(protocol.NPCDialogue(npcID, text, false, false))
		}
		return eventPortalNotice(actor.sink, text)
	}
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
		return notice("這裡已經調查過了。")
	}
	search.searched[name] = true
	if name == search.switchPortal {
		search.opened = true
	}
	s.events.mu.Unlock()
	if name == search.switchPortal {
		s.openRomeoLabDoor(inst)
		return notice("找到開關，研究室的門已打開。")
	}
	if name != search.letterPortal {
		return notice("這裡沒有發現可疑的東西。")
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
			return notice("請先保留一格其他欄位。")
		}
		return err
	}
	return notice("發現了一封和研究室有關的信。")
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
