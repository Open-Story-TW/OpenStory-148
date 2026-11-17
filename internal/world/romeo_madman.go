package world

import (
	"context"
	"math/rand/v2"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) romeoMadmanPortal(ctx context.Context, inst *eventInstance, actor eventOccupant, session domain.CharacterSession, portal content.Portal) (bool, error) {
	key := mapKey{inst.channel, session.Character.MapID}
	g := s.gameplay
	switch portal.Script {
	case "rnj6_act", "jnr6_act":
		stats := make([]content.Mob, 0, 5)
		for template := 9300142; template <= 9300146; template++ {
			def, err := g.catalog.Mob(template)
			if err != nil {
				return true, err
			}
			stats = append(stats, def)
		}
		g.mu.Lock()
		room := g.rooms[key]
		if room == nil || room.members[session.CharacterID] == nil || room.romeoWave {
			g.mu.Unlock()
			return true, nil
		}
		life, ok := romeoSpawnGround(room.definition, 115, 230)
		if !ok {
			g.mu.Unlock()
			return true, domain.ErrNotFound
		}
		now := time.Now()
		var updates []roomDelivery
		for i, definition := range stats {
			for j := 0; j < 10; j++ {
				spawn := life
				if candidate, ok := romeoSpawnGround(room.definition, -280+(i*10+j)*14, 230); ok {
					spawn = candidate
				}
				_, packets := g.spawnEventBossForm(room, spawn, definition, session.CharacterID, now)
				updates = append(updates, packets...)
			}
		}
		room.romeoWave = true
		room.romeoSwitch = 1 + rand.IntN(5)
		g.mu.Unlock()
		deliver(updates)
		return true, actor.sink.WritePacket(protocol.EnableActions())
	case "party5_click":
		if portal.Type != 11 || len(portal.Name) != len("click01") {
			return true, nil
		}
		g.mu.Lock()
		room := g.rooms[key]
		if !romeoWaveDefeated(room) || int(portal.Name[len(portal.Name)-1]-'0') != room.romeoSwitch {
			g.mu.Unlock()
			return true, eventPortalNotice(actor.sink, "這裡沒有找到開門的開關。")
		}
		var updates []roomDelivery
		for id, r := range room.reactors {
			if r.def.TemplateID == 2618005 {
				r.state = 1
				updates = roomBroadcast(room, protocol.ReactorState(id, 1, r.def.X, r.def.Y), -1)
				break
			}
		}
		g.mu.Unlock()
		deliver(updates)
		return true, eventPortalNotice(actor.sink, "找到了可疑的開關，門已經打開。")
	case "rnj6_out", "jnr6_out":
		g.mu.Lock()
		room := g.rooms[key]
		open := false
		if room != nil {
			for _, r := range room.reactors {
				if r.def.TemplateID == 2618005 && r.state == 1 {
					open = true
					break
				}
			}
		}
		g.mu.Unlock()
		if !open {
			return true, eventPortalNotice(actor.sink, "請擊退這間研究室的怪物，再找出可疑的開關。")
		}
		return true, s.romeoWarpNamed(ctx, inst, actor, key.mapID, inst.definition.Maps[0]+300, "sp")
	}
	return false, nil
}

func (s *Server) romeoMadmanNPC(inst *eventInstance, sink packetSink, mapID, npcID int) error {
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{inst.channel, mapID}]
	message := "請小心這裡的怪物。"
	beforeWave := room != nil && !room.romeoWave
	if romeoWaveDefeated(room) {
		message = "打開門的開關就藏在這間實驗室。"
	}
	g.mu.Unlock()
	if beforeWave {
		s.events.mu.Lock()
		if inst.romeoArchive.tendered&1 != 0 {
			inst.romeoAngered = true
			message = "你們竟然找到了那封信！"
		}
		s.events.mu.Unlock()
	}
	return sink.WritePacket(protocol.NPCDialogue(npcID, message, false, false))
}

func romeoWaveDefeated(room *gameplayRoom) bool {
	if room == nil || !room.romeoWave {
		return false
	}
	for _, mob := range room.mobs {
		if mob.hp > 0 || mob.claiming || mob.reservation != nil {
			return false
		}
	}
	return true
}

func romeoSpawnGround(field content.Map, x, y int) (content.Life, bool) {
	ground, ok := groundBelow(field, x, y)
	if !ok {
		return content.Life{}, false
	}
	for _, f := range field.Footholds {
		if f.X2 <= f.X1 || x < f.X1 || x > f.X2 {
			continue
		}
		if f.Y1+(f.Y2-f.Y1)*(x-f.X1)/(f.X2-f.X1) == ground {
			return content.Life{Kind: "m", X: x, Y: ground, Foothold: f.ID, RX0: f.X1, RX1: f.X2, RespawnSeconds: -1}, true
		}
	}
	return content.Life{}, false
}
