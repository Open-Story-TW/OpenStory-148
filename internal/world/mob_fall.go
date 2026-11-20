package world

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

// mobFallSlack is how far below a field's lowest foothold a controller may
// still report a mob (knockback, a jump-down in flight) before the server
// treats the mob as fallen out of the field.
const mobFallSlack = 100

// fieldFloor is the lowest foothold Y of a field; ok is false for a field
// without footholds, where no fall-out judgement is possible.
func fieldFloor(m content.Map) (floor int, ok bool) {
	for _, fh := range m.Footholds {
		y := max(fh.Y1, fh.Y2)
		if !ok || y > floor {
			floor, ok = y, true
		}
	}
	return floor, ok
}

type mobMoveVerdict int

const (
	mobMoveAccepted mobMoveVerdict = iota
	mobMoveRejected                // outside the patrol clamp: answered, not stored or relayed
	mobMoveFellOut                 // below the field: the mob is re-seated at its WZ origin
)

// applyMobMoveLocked judges a controller's movement report and stores the
// accepted end position. Caller holds gameplay.mu.
//
// The stored Y sits one pixel above the reported one, as Java
// MovementParse.updatePosition(res, monster, -1) does: the client rounds a
// mob standing on a slope to a pixel at or below the foothold line, and a mob
// re-spawned to a later entrant from that exact pixel finds no foothold under
// it and free-falls forever (104010000 slime slope, 2026-11-02).
func applyMobMoveLocked(r *gameplayRoom, mob *gameplayMob, m protocol.MonsterMove) mobMoveVerdict {
	if r.escort != nil && r.escort.mob == mob {
		route := r.definition.EscortRoute
		next := r.escort.next
		if next >= len(route) || r.escort.done {
			return mobMoveRejected
		}
		const slack = 150
		from := route[max(0, next-1)]
		if !r.escort.stopUntil.IsZero() {
			if within(m.X, m.Y, from.X, from.Y, slack) {
				mob.life.X, mob.life.Y = m.X, m.Y-1
				return mobMoveAccepted
			}
		} else {
			// A move can reach the following segment before its native node
			// acknowledgement is applied. Stops still cap that window.
			for _, to := range route[next:min(next+3, len(route))] {
				if m.X >= min(from.X, to.X)-slack && m.X <= max(from.X, to.X)+slack &&
					m.Y >= min(from.Y, to.Y)-slack && m.Y <= max(from.Y, to.Y)+slack {
					mob.life.X, mob.life.Y = m.X, m.Y-1
					return mobMoveAccepted
				}
				if to.Attribute == 2 && to.StopMillis > 0 {
					break
				}
				from = to
			}
		}
		return mobMoveRejected
	}
	if floor, ok := fieldFloor(r.definition); ok && m.Y > floor+mobFallSlack {
		return mobMoveFellOut
	}
	switch r.definition.ID {
	case content.KentaRescue, content.KentaBubbles, content.KentaDefense:
		if mob.stats.Flying {
			if !within(mob.life.X, mob.life.Y, m.X, m.Y, 320) || !insideSwimField(r.definition, m.X, m.Y) {
				return mobMoveRejected
			}
			mob.life.X, mob.life.Y = m.X, m.Y-1
			return mobMoveAccepted
		}
	}
	// Horizontal patrol bounds are WZ authority; clamp checks reject teleports.
	if m.X < mob.origin.RX0-100 || m.X > mob.origin.RX1+100 || m.Y < mob.origin.Y-400 || m.Y > mob.origin.Y+400 {
		return mobMoveRejected
	}
	mob.life.X, mob.life.Y = m.X, m.Y-1
	return mobMoveAccepted
}

type mobDelivery struct {
	sink   packetSink
	packet []byte
}

func insideSwimField(field content.Map, x, y int) bool {
	if len(field.Footholds) == 0 {
		return false
	}
	first := field.Footholds[0]
	bounds := content.MapArea{Left: min(first.X1, first.X2), Right: max(first.X1, first.X2),
		Top: min(first.Y1, first.Y2), Bottom: max(first.Y1, first.Y2)}
	for _, foothold := range field.Footholds[1:] {
		bounds.Left = min(bounds.Left, foothold.X1, foothold.X2)
		bounds.Right = max(bounds.Right, foothold.X1, foothold.X2)
		bounds.Top = min(bounds.Top, foothold.Y1, foothold.Y2)
		bounds.Bottom = max(bounds.Bottom, foothold.Y1, foothold.Y2)
	}
	const edgeSlack = 100
	return x >= bounds.Left-edgeSlack && x <= bounds.Right+edgeSlack &&
		y >= bounds.Top-edgeSlack && y <= bounds.Bottom+edgeSlack
}

// respawnMobLocked gives a mob a fresh object ID at its WZ origin with full
// HP and no statuses, and returns the spawn packets for the room. The first
// member becomes its controller. Caller holds gameplay.mu.
func (g *gameplaySpace) respawnMobLocked(r *gameplayRoom, mob *gameplayMob) ([]mobDelivery, bool) {
	if mob.qaOwner != nil {
		return nil, false
	} // QA summons are one real spawn, never a respawn source.
	newID := g.alloc()
	if newID == 0 {
		return nil, false
	}
	delete(r.mobs, mob.objectID)
	mob.objectID = newID
	mob.life = mob.origin
	mob.hp = mob.stats.HP
	mob.respawnAt = time.Time{}
	mob.escortHitAt = time.Time{}
	mob.controller = 0
	mob.contributions = nil
	mob.reservation = nil
	mob.statuses = nil
	mob.burns = nil
	mob.nextMobContact = time.Time{}
	mob.cast = mobPowerUpCast{}
	r.mobs[mob.objectID] = mob
	var out []mobDelivery
	for id, m := range r.members {
		out = append(out, mobDelivery{m.sink, protocol.SpawnMonster(mob.objectID, mob.life, false)})
		if mob.controller == 0 {
			mob.controller = id
			out = append(out, mobDelivery{m.sink, protocol.SpawnMonster(mob.objectID, mob.life, true)})
		}
	}
	return out, true
}

// reseatFallenMobLocked removes a mob its controller reported below the field
// and respawns it at its WZ origin, so one bad landing cannot leave a spawn
// unreachable until the next restart. A mob inside a combat settlement is
// left alone; the next movement report retries. Caller holds gameplay.mu.
func (g *gameplaySpace) reseatFallenMobLocked(r *gameplayRoom, mob *gameplayMob) []mobDelivery {
	if mob.claiming || mob.reservation != nil {
		return nil
	}
	oldID := mob.objectID
	if mob.qaOwner != nil {
		delete(r.mobs, oldID)
		var out []mobDelivery
		for _, m := range r.members {
			out = append(out, mobDelivery{m.sink, protocol.RemoveMonster(oldID)})
		}
		return out
	}
	out, ok := g.respawnMobLocked(r, mob)
	if !ok {
		return nil
	}
	var removes []mobDelivery
	for _, m := range r.members {
		removes = append(removes, mobDelivery{m.sink, protocol.RemoveMonster(oldID)})
	}
	return append(removes, out...)
}
