package world

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

const crimsonBalrog = 8150000

type boatInvasion struct {
	mobs    []*gameplayMob
	visible bool
}

func (s *Server) updateBoatDock(channel, dock int, phase transitPhase) {
	if s.boatPhases == nil {
		return
	}
	key := mapKey{channel, dock}
	previous, known := s.boatPhases[key]
	s.boatPhases[key] = phase
	if !known || phase == previous {
		return
	}
	if phase == transitAway {
		s.broadcastInventoryPacket(channel, dock, protocol.BoatDepart())
	} else if previous == transitAway {
		s.broadcastInventoryPacket(channel, dock, protocol.BoatArrive())
	}
}

func (s *Server) boatStateRequest(channel int, live *liveCharacter, sink packetSink, payload []byte) error {
	if len(payload) != 5 || s.gameplay == nil {
		return nil
	}
	r := protocol.NewReader(payload)
	mapID, renderKind := int(r.I32()), r.U8()
	visit := s.sceneFor(live, sink)
	if visit == nil || mapID != visit.owner.Character.MapID || renderKind > 1 {
		return nil
	}
	for _, line := range transitLines {
		for _, leg := range line.legs {
			if leg.cabin == 0 {
				continue
			}
			switch mapID {
			case leg.dock:
				docked := line.phase(s.transitStart, time.Now()) != transitAway
				return s.writeScenePackets(visit, protocol.BoatState(docked, false))
			case leg.ride:
				return s.writeScenePackets(visit, protocol.BoatState(false, s.boatInvaded(channel, mapID)))
			}
		}
	}
	return nil
}

func (s *Server) boatInvaded(channel, deck int) bool {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	if room := g.rooms[mapKey{channel, deck}]; room != nil {
		for _, mob := range room.mobs {
			if mob.oneTime && mob.life.TemplateID == crimsonBalrog && mob.hp > 0 {
				return true
			}
		}
	}
	return false
}

func (s *Server) spawnBoatInvasion(channel, deck int) {
	key := mapKey{channel: channel, mapID: deck}
	if s.boatInvaders == nil {
		return
	}
	if invasion := s.boatInvaders[key]; invasion != nil {
		if invasion.visible && !s.boatInvaded(channel, deck) {
			invasion.visible = false
			s.broadcastInventoryPacket(channel, deck, protocol.BoatInvadersLeave())
		}
		return
	}
	// The original spawns are on foothold 10 of the two deck maps.
	x, y := -538, 143
	if deck == 200090010 {
		x, y = 339, 155
	}
	g := s.gameplay
	stats, err := g.catalog.Mob(crimsonBalrog)
	if err != nil {
		return
	}
	type delivery struct {
		visit  *sceneVisit
		packet []byte
	}
	var deliveries []delivery
	invasion := &boatInvasion{visible: true}
	g.mu.Lock()
	room := g.rooms[key]
	if room != nil {
		life := content.Life{Kind: "m", TemplateID: crimsonBalrog, X: x, Y: y,
			Foothold: 10, RX0: x - 240, RX1: x + 240, RespawnSeconds: -1}
		for range 2 {
			oid := g.alloc()
			if oid == 0 {
				break
			}
			mob := &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP, oneTime: true}
			room.mobs[oid] = mob
			invasion.mobs = append(invasion.mobs, mob)
			spawn := protocol.SpawnMonster(oid, life, false)
			for id, member := range room.members {
				deliveries = append(deliveries, delivery{member.visit, spawn})
				if mob.controller == 0 {
					mob.controller = id
					deliveries = append(deliveries, delivery{member.visit, protocol.SpawnMonster(oid, life, true)})
				}
			}
		}
	}
	g.mu.Unlock()
	if len(invasion.mobs) > 0 {
		s.boatInvaders[key] = invasion
		s.broadcastInventoryPacket(channel, deck, protocol.BoatInvadersArrive())
	}
	for _, delivery := range deliveries {
		_ = s.writeScenePackets(delivery.visit, delivery.packet)
	}
}

func (s *Server) clearBoatInvasion(channel, deck int) {
	key := mapKey{channel: channel, mapID: deck}
	invasion := s.boatInvaders[key]
	if invasion == nil {
		return
	}
	delete(s.boatInvaders, key)
	var packets [][]byte
	if invasion.visible {
		packets = append(packets, protocol.BoatInvadersLeave())
	}
	var visits []*sceneVisit
	g := s.gameplay
	g.mu.Lock()
	if room := g.rooms[key]; room != nil {
		for _, mob := range invasion.mobs {
			if room.mobs[mob.objectID] == mob {
				if mob.hp > 0 {
					packets = append(packets, protocol.KillMonster(mob.objectID))
				}
				delete(room.mobs, mob.objectID)
			}
		}
		for _, member := range room.members {
			visits = append(visits, member.visit)
		}
	}
	g.mu.Unlock()
	for _, visit := range visits {
		_ = s.writeScenePackets(visit, packets...)
	}
}
