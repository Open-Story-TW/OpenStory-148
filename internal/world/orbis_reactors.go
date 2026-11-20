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

var orbisStatueSockets = map[int]int{
	2008000: 4001046,
	2008001: 4001049,
	2008004: 4001048,
	2008005: 4001045,
}

const (
	orbisStatue = 2006001
	orbisPlayer = 2008006
	orbisLever  = 2008007
)

func orbisReactor(room *gameplayRoom, template int) *roomReactor {
	if room != nil {
		for _, reactor := range room.reactors {
			if reactor.def.TemplateID == template {
				return reactor
			}
		}
	}
	return nil
}

func (s *Server) hitOrbisLever(channel int, live *liveCharacter, sink packetSink, oid int32) {
	session := partySession(live)
	if session.Character.MapID != content.OrbisPQAscent {
		return
	}
	visit := s.sceneFor(live, sink)
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.orbis == nil || visit == nil || session.Character.HP <= 0 || s.tradeActive(live) {
		return
	}
	game := s.gameplay
	game.mu.Lock()
	room := game.rooms[mapKey{channel, content.OrbisPQAscent}]
	if room == nil || room.members[session.CharacterID] == nil || room.members[session.CharacterID].visit != visit {
		game.mu.Unlock()
		return
	}
	lever := room.reactors[oid]
	if lever == nil || lever.def.TemplateID != orbisLever ||
		!within(session.Character.X, session.Character.Y, lever.def.X, lever.def.Y, 140) || time.Since(lever.lastHit) < 250*time.Millisecond {
		game.mu.Unlock()
		return
	}
	lever.state = 1 - lever.state
	lever.lastHit = time.Now()
	updates := roomBroadcast(room, protocol.ReactorState(oid, lever.state, lever.def.X, lever.def.Y), -1)
	game.mu.Unlock()
	deliver(updates)
}

func (s *Server) clickOrbisReactor(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) error {
	session := partySession(live)
	mapID := session.Character.MapID
	if mapID != content.OrbisPQMusic && mapID != content.OrbisPQHall && mapID != content.OrbisPQGarden {
		return nil
	}
	inst := s.eventOf(session.CharacterID)
	visit := s.sceneFor(live, sink)
	if inst == nil || inst.orbis == nil || visit == nil || session.Character.HP <= 0 || s.tradeActive(live) {
		return nil
	}
	game := s.gameplay
	key := mapKey{channel, mapID}
	game.mu.Lock()
	room := game.rooms[key]
	if room == nil || room.members[session.CharacterID] == nil || room.members[session.CharacterID].visit != visit {
		game.mu.Unlock()
		return nil
	}
	reactor := room.reactors[oid]
	if reactor == nil || reactor.finishing || !within(session.Character.X, session.Character.Y, reactor.def.X, reactor.def.Y, 320) {
		game.mu.Unlock()
		return nil
	}
	def := reactor.def
	var items []domain.ScriptItemDelta
	var drops []domain.ScriptDrop
	var plant *gameplayMob
	var minervaID int32
	next := 1

	switch {
	case mapID == content.OrbisPQMusic && def.TemplateID >= 2002004 && def.TemplateID <= 2002010 && reactor.state == 0:
		ground, ok := groundBelow(room.definition, def.X, def.Y)
		if !ok {
			ground = def.Y
		}
		drops = []domain.ScriptDrop{{TemplateID: 4001056 + def.TemplateID - 2002004, Quantity: 1, X: def.X, Y: ground}}
	case mapID == content.OrbisPQHall && orbisStatueSockets[def.TemplateID] != 0 && reactor.state == 0:
		items = []domain.ScriptItemDelta{{TemplateID: orbisStatueSockets[def.TemplateID], Quantity: -1}}
	case mapID == content.OrbisPQHall && def.TemplateID == orbisStatue && reactor.state == len(orbisStatueSockets):
		items = []domain.ScriptItemDelta{{TemplateID: 4001055, Quantity: -1}}
		next = len(orbisStatueSockets) + 1
		minervaID = game.alloc()
		if minervaID == 0 {
			game.mu.Unlock()
			return nil
		}
	case mapID == content.OrbisPQGarden && (def.TemplateID == 2001000 || def.TemplateID == 2001001) && reactor.state == 0:
		items = []domain.ScriptItemDelta{{TemplateID: 4001053, Quantity: -1}}
		template := 9300048
		if def.TemplateID == 2001001 {
			template = 9300049
		}
		plant = game.orbisMob(room, template, def.X, def.Y)
		if plant == nil {
			game.mu.Unlock()
			return nil
		}
	default:
		game.mu.Unlock()
		return nil
	}
	reactor.finishing = true
	game.mu.Unlock()

	op := fmt.Sprintf("orbis-%s-reactor-%d", inst.id, oid)
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, op, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || current.ChannelID != channel || current.Character.HP <= 0 ||
			s.eventOf(current.CharacterID) != inst || s.sceneFor(live, sink) != visit {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		return domain.ScriptExchange{Items: items, Drops: drops}, nil
	})
	game.mu.Lock()
	reactor.finishing = false
	if err != nil || game.rooms[key] != room {
		game.mu.Unlock()
		if errors.Is(err, domain.ErrInsufficient) {
			return sink.WritePacket(protocol.PortalTrainingMessage("你身上沒有機關所需的道具。"))
		}
		return err
	}
	reactor.state = next
	updates := roomBroadcast(room, protocol.ReactorState(oid, next, def.X, def.Y), -1)
	if orbisStatueSockets[def.TemplateID] != 0 {
		if statue := orbisReactor(room, orbisStatue); statue != nil && statue.state < len(orbisStatueSockets) {
			statue.state++
			updates = append(updates, roomBroadcast(room, protocol.ReactorState(statue.objectID, statue.state, statue.def.X, statue.def.Y), -1)...)
		}
	}
	if minervaID != 0 {
		life := content.Life{Kind: "n", TemplateID: 2013002, X: def.X, Y: def.Y, RX0: def.X - 40, RX1: def.X + 40}
		room.npcs[minervaID] = life
		for _, packet := range s.npcScriptability(map[int32]content.Life{minervaID: life}) {
			updates = append(updates, roomBroadcast(room, packet, -1)...)
		}
		updates = append(updates, roomBroadcast(room, protocol.SpawnNPC(minervaID, life, false), -1)...)
		updates = append(updates, roomBroadcast(room, protocol.SpawnNPC(minervaID, life, true), -1)...)
	}
	if plant != nil {
		updates = append(updates, spawnOrbisMob(room, plant)...)
	}
	game.mu.Unlock()
	deliver(updates)
	return nil
}

func (s *Server) offerOrbisRecord(ctx context.Context, session domain.CharacterSession, drop domain.ItemDrop) {
	if drop.MapID != content.OrbisPQMusic || session.Character.MapID != drop.MapID || drop.ChannelID != session.ChannelID ||
		drop.OwnerID != session.CharacterID || !drop.PlayerDrop || drop.Quantity != 1 {
		return
	}
	day := int(time.Now().In(time.FixedZone("Taiwan", 8*3600)).Weekday())
	if drop.TemplateID != 4001056+day {
		return
	}
	inst := s.eventOf(session.CharacterID)
	writer, ok := s.repo.(domain.EventDropRepository)
	if inst == nil || inst.orbis == nil || !ok {
		return
	}
	game := s.gameplay
	key := mapKey{channel: session.ChannelID, mapID: drop.MapID}
	game.mu.Lock()
	room := game.rooms[key]
	player := orbisReactor(room, orbisPlayer)
	area := content.MapArea{Left: -52, Top: -64, Right: 40, Bottom: 79}
	if player == nil || room.members[session.CharacterID] == nil || player.state != 0 || player.finishing ||
		!area.Contains(drop.X-player.def.X, drop.Y-player.def.Y) {
		game.mu.Unlock()
		return
	}
	player.finishing = true
	game.mu.Unlock()

	consumed, err := writer.ConsumeEventDrop(ctx, drop)
	game.mu.Lock()
	player.finishing = false
	if err != nil || !consumed || game.rooms[key] != room {
		game.mu.Unlock()
		if consumed {
			s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
		}
		return
	}
	player.state = 1 + day
	updates := roomBroadcast(room, protocol.ReactorState(player.objectID, player.state, player.def.X, player.def.Y), -1)
	game.mu.Unlock()
	s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
	deliver(updates)
}

func (g *gameplaySpace) orbisMob(room *gameplayRoom, template, x, y int) *gameplayMob {
	stats, err := g.catalog.Mob(template)
	if err != nil {
		return nil
	}
	objectID := g.alloc()
	if objectID == 0 {
		return nil
	}
	if ground, ok := groundBelow(room.definition, x, y); ok {
		y = ground
	}
	life := content.Life{Kind: "m", TemplateID: template, X: x, Y: y, RespawnSeconds: -1}
	return &gameplayMob{objectID: objectID, life: life, origin: life, stats: stats, hp: stats.HP, oneTime: true}
}

func spawnOrbisMob(room *gameplayRoom, mob *gameplayMob) []roomDelivery {
	room.mobs[mob.objectID] = mob
	now := time.Now()
	updates := roomBroadcast(room, mob.spawnPacket(false, now), -1)
	for actorID, member := range room.members {
		mob.controller = actorID
		updates = append(updates, roomDelivery{member.sink, mob.spawnPacket(true, now)})
		break
	}
	return updates
}
