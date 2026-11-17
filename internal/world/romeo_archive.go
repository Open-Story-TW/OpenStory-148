package world

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const (
	romeoAccessCard = 4001133
	romeoAlcadno    = 4001134
	romeoZenumist   = 4001135
)

type romeoLabArchive struct {
	tendered uint8
	reserved uint8
}

func (s *Server) romeoArchivePortal(ctx context.Context, inst *eventInstance, actor eventOccupant, current int, portal content.Portal) (bool, error) {
	first := inst.definition.Maps[0]
	var target int
	var reactor int
	switch portal.Script {
	case "rnj3_in0", "jnr3_in0":
		target, reactor = first+201, 2618002
	case "rnj3_in1", "jnr3_in1":
		target, reactor = first+202, 2618001
	case "rnj_201_0", "jnr_201_0", "rnj_202", "jnr_202":
		target = first + 200
	case "rnj3_out", "jnr3_out":
		target, reactor = first+203, 2618007
	default:
		return false, nil
	}
	if reactor != 0 {
		g := s.gameplay
		g.mu.Lock()
		open := false
		if room := g.rooms[mapKey{inst.channel, current}]; room != nil {
			for _, r := range room.reactors {
				if r.def.TemplateID == reactor && r.state > 0 {
					open = true
					break
				}
			}
		}
		g.mu.Unlock()
		if !open {
			return true, eventPortalNotice(actor.sink, "這道門還沒有打開。")
		}
	}
	return true, s.romeoWarpNamed(ctx, inst, actor, current, target, "sp")
}

func (s *Server) clickRomeoArchive(ctx context.Context, live *liveCharacter, sink packetSink, objectID int32) (bool, error) {
	session := partySession(live)
	mapID := session.Character.MapID
	switch mapID {
	case 926100200, 926100201, 926100202, 926110200, 926110201, 926110202:
	default:
		return false, nil
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || !inst.definition.Stage(mapID) || session.Character.HP <= 0 {
		return true, nil
	}
	key := mapKey{inst.channel, mapID}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[session.CharacterID] == nil {
		g.mu.Unlock()
		return true, nil
	}
	r := room.reactors[objectID]
	if r == nil || r.state != 0 || r.finishing || !within(session.Character.X, session.Character.Y, r.def.X, r.def.Y, 145) {
		g.mu.Unlock()
		return true, nil
	}
	var item, quantity int
	switch r.def.TemplateID {
	case 2618001, 2618002:
		item, quantity = romeoAccessCard, -1
	case 2612001:
		item, quantity = romeoAlcadno, 1
	case 2612002:
		item, quantity = romeoZenumist, 1
	case 2612003:
		r.state = 1
		updates := roomBroadcast(room, protocol.ReactorState(objectID, 1, r.def.X, r.def.Y), -1)
		g.mu.Unlock()
		deliver(updates)
		return true, eventPortalNotice(sink, "箱子是空的。")
	default:
		g.mu.Unlock()
		return true, nil
	}
	r.finishing = true
	g.mu.Unlock()
	op := fmt.Sprintf("rnj-archive-%s-%d", inst.id, objectID)
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, op, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: item, Quantity: quantity}}}, nil
	})
	g.mu.Lock()
	r.finishing = false
	if err != nil || g.rooms[key] != room {
		g.mu.Unlock()
		if errors.Is(err, domain.ErrInsufficient) {
			message := "請先保留一格其他欄位。"
			if quantity < 0 {
				message = "需要一張門禁卡。"
			}
			return true, eventPortalNotice(sink, message)
		}
		return true, err
	}
	r.state = 1
	updates := roomBroadcast(room, protocol.ReactorState(objectID, 1, r.def.X, r.def.Y), -1)
	g.mu.Unlock()
	deliver(updates)
	return true, nil
}

func (s *Server) romeoArchiveNPC(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	session := partySession(live)
	mapID := session.Character.MapID
	var npcID, letter int
	switch mapID {
	case 926100200:
		npcID, letter = 2112006, 4001130
	case 926110200:
		npcID, letter = 2112005, 4001131
	case 926100203:
		npcID = 2112000
	case 926110203:
		npcID = 2112010
	default:
		return false, nil
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || !inst.definition.Stage(mapID) || session.Character.HP <= 0 || s.tradeActive(live) {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	g := s.gameplay
	g.mu.Lock()
	var npc content.Life
	if room := g.rooms[mapKey{channel, mapID}]; room != nil {
		npc = room.npcs[oid]
	}
	g.mu.Unlock()
	if npc.TemplateID != npcID || !npcReach(session.Character.X, session.Character.Y, npc.X, npc.Y) {
		return false, nil
	}
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	if npcID == 2112000 || npcID == 2112010 {
		return true, s.romeoMadmanNPC(inst, sink, mapID, npcID)
	}
	items := []int{letter, romeoAlcadno, romeoZenumist}
	var offered uint8
	var item int
	s.events.mu.Lock()
	for i, id := range items {
		flag := uint8(1 << i)
		hasItem := slices.ContainsFunc(session.Character.Items, func(item domain.InventoryItem) bool {
			return item.TemplateID == id && item.Quantity > 0
		})
		if (inst.romeoArchive.tendered|inst.romeoArchive.reserved)&flag == 0 && hasItem {
			offered, item = flag, id
			break
		}
	}
	inst.romeoArchive.reserved |= offered
	s.events.mu.Unlock()
	if offered == 0 {
		return true, sink.WritePacket(protocol.NPCDialogue(npcID, "請從兩側的研究室帶回卡帕萊特和蒙特鳩的研究資料。", false, false))
	}
	op := fmt.Sprintf("rnj-report-%s-%d", inst.id, item)
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, op, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: item, Quantity: -1}}}, nil
	})
	s.events.mu.Lock()
	inst.romeoArchive.reserved &^= offered
	wasOpen := inst.romeoArchive.tendered&6 == 6
	if err == nil {
		inst.romeoArchive.tendered |= offered
	}
	open := inst.romeoArchive.tendered&6 == 6
	s.events.mu.Unlock()
	if err != nil {
		if errors.Is(err, domain.ErrInsufficient) {
			return true, sink.WritePacket(protocol.EnableActions())
		}
		return true, err
	}
	message := "我收到資料了，還差另一間研究室的資料。"
	if offered == 1 {
		message = "謝謝你送回這封信。我想請你們把它的內容轉告研究室裡的烏里特。"
	} else if open {
		message = "兩份資料已經齊全，通往下一間研究室的門開了。"
	}
	if open && !wasOpen {
		g.mu.Lock()
		var updates []roomDelivery
		if room := g.rooms[mapKey{channel, mapID}]; room != nil {
			for id, reactor := range room.reactors {
				if reactor.def.TemplateID == 2618007 {
					reactor.state = 1
					updates = roomBroadcast(room, protocol.ReactorState(id, 1, reactor.def.X, reactor.def.Y), -1)
					break
				}
			}
		}
		g.mu.Unlock()
		deliver(updates)
	}
	return true, sink.WritePacket(protocol.NPCDialogue(npcID, message, false, false))
}
