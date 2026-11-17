package world

import (
	"fmt"
	"math/rand/v2"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

func judgementMobPoints(id int) int {
	switch id {
	case 9300152, 9300039, 9300105, 9300012:
		return 15
	case 9300136:
		return 5
	default:
		return 1
	}
}

func (g *gameplaySpace) startJudgementLocked(key mapKey, room *gameplayRoom, now time.Time) ([]judgementPacket, error) {
	// The v148 arena floor replaces the original script's out-of-bounds
	// y=100 anchor, retaining its full-width distribution.
	var floor content.Foothold
	for _, f := range room.definition.Footholds {
		if f.X1 < -600 && f.X2 > 850 && f.Y1 == f.Y2 {
			floor = f
			break
		}
	}
	if floor.ID == 0 {
		return nil, fmt.Errorf("Field of Judgement arena has no lower floor")
	}
	var controller int64
	for _, id := range room.judgement.entrants {
		if room.members[id] != nil {
			controller = id
			break
		}
	}
	mobs := make(map[int32]*gameplayMob)
	for _, wave := range content.JudgementWaves(key.mapID) {
		stats, err := g.catalog.Mob(wave.TemplateID)
		if err != nil {
			return nil, err
		}
		stats = wave.Stats(stats)
		for i := 0; i < wave.Count; i++ {
			oid := g.alloc()
			if oid == 0 {
				return nil, fmt.Errorf("Field of Judgement object IDs exhausted")
			}
			x := floor.X1 + 4 + rand.IntN(floor.X2-floor.X1-8)
			life := content.Life{Kind: "m", TemplateID: wave.TemplateID, X: x, Y: floor.Y1, Foothold: floor.ID, RX0: floor.X1, RX1: floor.X2, RespawnSeconds: -1}
			mobs[oid] = &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP, controller: controller, oneTime: true}
		}
	}
	room.mobs = mobs
	ends := room.judgement.setup.Add(judgementStart + judgementDuration)
	seconds := int((ends.Sub(now) + time.Second - 1) / time.Second)
	var out []judgementPacket
	for id, member := range room.members {
		out = append(out, judgementPacket{visit: member.visit, packet: protocol.Clock(seconds)})
		for _, mob := range room.mobs {
			out = append(out, judgementPacket{visit: member.visit, packet: mob.spawnPacket(false, now)})
			if id == controller {
				out = append(out, judgementPacket{visit: member.visit, packet: mob.spawnPacket(true, now)})
			}
		}
	}
	return out, nil
}
