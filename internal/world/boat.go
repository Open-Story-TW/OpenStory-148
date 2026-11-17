package world

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

const crimsonBalrog = 8150000

func (s *Server) spawnBoatInvasion(channel, deck int) {
	key := mapKey{channel: channel, mapID: deck}
	if s.boatInvaders == nil || s.boatInvaders[key] != nil {
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
			s.boatInvaders[key] = append(s.boatInvaders[key], mob)
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
	for _, delivery := range deliveries {
		_ = s.writeScenePackets(delivery.visit, delivery.packet)
	}
}

func (s *Server) clearBoatInvasion(channel, deck int) {
	key := mapKey{channel: channel, mapID: deck}
	invaders := s.boatInvaders[key]
	if len(invaders) == 0 {
		return
	}
	delete(s.boatInvaders, key)
	var packets [][]byte
	var visits []*sceneVisit
	g := s.gameplay
	g.mu.Lock()
	if room := g.rooms[key]; room != nil {
		for _, mob := range invaders {
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
