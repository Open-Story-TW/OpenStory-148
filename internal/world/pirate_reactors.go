package world

import (
	"context"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (g *gameplaySpace) addPirateReactors(room *gameplayRoom) error {
	if room == nil {
		return domain.ErrNotFound
	}
	defs, err := content.PiratePQReactors(wzRoot(), room.definition.ID)
	if err != nil {
		return err
	}
	if room.reactors == nil {
		room.reactors = make(map[int32]*roomReactor)
	}
	for _, def := range defs {
		oid := g.alloc()
		if oid == 0 {
			return domain.ErrForbidden
		}
		room.reactors[oid] = &roomReactor{objectID: oid, def: def}
	}
	return nil
}

func (s *Server) pirateDoorsClosed(channel int) bool {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	if room := g.rooms[mapKey{channel, content.PiratePQDoors}]; room != nil {
		closed := 0
		for _, r := range room.reactors {
			if r.def.TemplateID >= 2519000 && r.def.TemplateID <= 2519003 && r.state == 1 {
				closed++
			}
		}
		return closed == 4
	}
	return false
}

func (s *Server) pirateReactorHit(channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	actor := partySession(live)
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.pirate == nil || inst.channel != channel {
		return false, nil
	}
	inst.pirate.mu.Lock()
	defer inst.pirate.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.currentDeadline()) {
		return true, nil
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, actor.Character.MapID}]
	if room == nil || room.members[actor.CharacterID] == nil || !sameDropSink(room.members[actor.CharacterID].sink, sink) {
		g.mu.Unlock()
		return true, nil
	}
	reactor := room.reactors[oid]
	if reactor == nil || reactor.state != 0 || actor.Character.HP <= 0 ||
		!within(actor.Character.X, actor.Character.Y, reactor.def.X, reactor.def.Y, 170) {
		g.mu.Unlock()
		return false, nil
	}
	var mobs map[int]int
	switch reactor.def.TemplateID {
	case 2511000:
		mobs = map[int]int{9300109: 3, 9300110: 5}
	case 2511001:
		mobs = map[int]int{9300124: 6, 9300125: 6}
	case 2518000:
		mobs = map[int]int{9300108: 3, 9300109: 3, 9300110: 3, 9300111: 3}
	default:
		g.mu.Unlock()
		return false, nil
	}
	var spawned []*gameplayMob
	for id, count := range mobs {
		for i := 0; i < count; i++ {
			mob, err := g.pirateSpawn(room, id, reactor.def.X, reactor.def.Y-45)
			if err != nil {
				for _, m := range spawned {
					delete(room.mobs, m.objectID)
				}
				g.mu.Unlock()
				return true, err
			}
			spawned = append(spawned, mob)
		}
	}
	reactor.state = 1
	packets := roomBroadcast(room, protocol.ReactorState(oid, 1, reactor.def.X, reactor.def.Y), -1)
	for _, mob := range spawned {
		mob.controller = actor.CharacterID
		packets = append(packets, roomBroadcast(room, mob.spawnPacket(false, time.Now()), -1)...)
		packets = append(packets, roomDelivery{sink, mob.spawnPacket(true, time.Now())})
	}
	g.mu.Unlock()
	deliver(packets)
	return true, nil
}

func pirateDropFits(def content.Reactor, d domain.ItemDrop) bool {
	x, y := d.X-def.X, d.Y-def.Y
	if def.TemplateID == 2512001 {
		return x >= -69 && x <= 7 && y >= -50 && y <= 8
	}
	return x >= -35 && x <= 40 && y >= -91 && y <= 5
}

func (s *Server) tickPiratePQ(ctx context.Context, now time.Time) {
	if s.events == nil {
		return
	}
	s.events.mu.Lock()
	var active []*eventInstance
	for _, inst := range s.events.running {
		if inst.pirate != nil && !inst.ended && now.Before(inst.currentDeadline()) {
			active = append(active, inst)
		}
	}
	s.events.mu.Unlock()
	for _, inst := range active {
		for _, mapID := range []int{content.PiratePQMedals, content.PiratePQLower + 1, content.PiratePQUpper + 1, content.PiratePQDoors} {
			if len(s.eventOccupants(inst.channel, []int{mapID})) != 0 {
				step, cancel := context.WithTimeout(ctx, 550*time.Millisecond)
				s.usePirateDrops(step, inst, mapID)
				cancel()
			}
		}
	}
}

func (s *Server) usePirateDrops(ctx context.Context, inst *eventInstance, mapID int) {
	reader, readable := s.repo.(domain.InventoryRepository)
	writer, writable := s.repo.(domain.EventDropRepository)
	if !readable || !writable {
		return
	}
	inst.pirate.mu.Lock()
	defer inst.pirate.mu.Unlock()
	if !time.Now().Before(inst.currentDeadline()) {
		return
	}
	drops, err := reader.ListItemDrops(ctx, s.cfg.WorldID, inst.channel, mapID)
	if err != nil {
		return
	}
	g := s.gameplay
	for _, d := range drops {
		if !d.PlayerDrop || d.Quantity != 1 || s.eventOf(d.OwnerID) != inst {
			continue
		}
		g.mu.Lock()
		room := g.rooms[mapKey{inst.channel, mapID}]
		var match *roomReactor
		if room != nil {
			for _, r := range room.reactors {
				if r.state == 0 && content.PirateReactorItem(r.def.TemplateID) == d.TemplateID && pirateDropFits(r.def, d) {
					match = r
					break
				}
			}
		}
		g.mu.Unlock()
		if match == nil {
			continue
		}
		applied, err := writer.ConsumeEventDrop(ctx, d)
		if err != nil || !applied {
			continue
		}
		s.removeVisibleDrop(d, protocol.RemoveItemExpired(d.ObjectID))
		g.mu.Lock()
		match.state = 1
		if match.def.TemplateID == 2512001 {
			match.state = 2
			inst.pirate.chests++
		}
		packets := roomBroadcast(room, protocol.ReactorState(match.objectID, match.state, match.def.X, match.def.Y), -1)
		g.mu.Unlock()
		deliver(packets)
	}
}
