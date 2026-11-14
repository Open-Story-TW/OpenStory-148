package world

import (
	"errors"
	"maple-go-server/internal/content"
	"math"
)

type snailPosition struct{ X, Y int }
type snailWorldRect struct{ left, top, right, bottom int64 }

type snailBodySnapshot struct {
	mob    *gameplayMob
	bounds content.MobBodyBounds
}

// Resolve immutable content outside the room lock, then bind it to the exact
// spawn pointer again at admission. A respawn, leave or missing asset cannot
// borrow another object's body. This routine never reads mutable coordinates
// after dropping the lock.
func (g *gameplaySpace) snailBodies(key mapKey, objectIDs []int32) map[int32]snailBodySnapshot {
	type candidate struct {
		id    int32
		mob   *gameplayMob
		mobID int
	}
	var candidates []candidate
	g.mu.Lock()
	if room := g.rooms[key]; room != nil {
		for _, id := range objectIDs {
			if mob := room.mobs[id]; mob != nil {
				candidates = append(candidates, candidate{id, mob, mob.stats.ID})
			}
		}
	}
	g.mu.Unlock()
	result := make(map[int32]snailBodySnapshot, len(candidates))
	for _, c := range candidates {
		if body, err := g.catalog.MobBodyBounds(c.mobID); err == nil {
			result[c.id] = snailBodySnapshot{c.mob, body}
		}
	}
	return result
}

// snailGeometryAdmits is conservative server admission, not native selection.
// Caller supplies a known static WZ envelope and trusted current/optional prior
// positions. No client-provided rectangle, padding or extra range is accepted.
// In particular, the caller must fence the age/ownership of its position history.
// It cannot prove current animation frame, first eligible target, or client FX.
func snailGeometryAdmits(actor snailPosition, left bool, body content.MobBodyBounds, current snailPosition, previous *snailPosition) (bool, error) {
	unknown := errors.New("unknown or invalid Three Snails geometry")
	if body.Kind != "static_frame_envelope_both_facings" || body.FrameCount < 1 || !body.Envelope.Nonempty() {
		return false, unknown
	}
	valid := func(p snailPosition) bool {
		return int64(p.X) >= math.MinInt32 && int64(p.X) <= math.MaxInt32 && int64(p.Y) >= math.MinInt32 && int64(p.Y) <= math.MaxInt32
	}
	if !valid(actor) || !valid(current) || previous != nil && !valid(*previous) {
		return false, unknown
	}
	if int64(actor.X)-300 < math.MinInt32 || int64(actor.X)+300 > math.MaxInt32 || int64(actor.Y)-100 < math.MinInt32 || int64(actor.Y)+44 > math.MaxInt32 {
		return false, unknown
	}
	r := body.Envelope
	for _, v := range []int{r.Left, r.Top, r.Right, r.Bottom} {
		if v < -4096 || v > 4096 {
			return false, unknown
		}
	}
	offset := func(p snailPosition) snailWorldRect {
		return snailWorldRect{int64(p.X) + int64(r.Left), int64(p.Y) + int64(r.Top), int64(p.X) + int64(r.Right), int64(p.Y) + int64(r.Bottom)}
	}
	target := offset(current)
	validRect := func(r snailWorldRect) bool {
		return r.left >= math.MinInt32 && r.right <= math.MaxInt32 && r.top >= math.MinInt32 && r.bottom <= math.MaxInt32
	}
	if !validRect(target) {
		return false, unknown
	}
	if previous != nil {
		old := offset(*previous)
		if !validRect(old) {
			return false, unknown
		}
		target = snailWorldRect{min(target.left, old.left), min(target.top, old.top), max(target.right, old.right), max(target.bottom, old.bottom)}
	}
	// 884233/884258: GetPos.x, GetPos.y-28. 884B3F/884B76 call
	// 5E7813 with muzzle +/-50, endpoint +/-300 and divisor4. That helper
	// uses each slice's NEAR distance for integer half-height, not its far edge.
	originX, originY := int64(actor.X), int64(actor.Y)-28
	for distance := int64(50); distance < 300; distance += 20 {
		far := min(distance+20, 300)
		lo, hi := originX+distance, originX+far
		if left {
			lo, hi = originX-far, originX-distance
		}
		half := distance / 4
		// Win32 IntersectRect requires positive area: merely touching an edge
		// (including the x=+/-300 endpoint) is not an intersection.
		if max(lo, target.left) < min(hi, target.right) && max(originY-half, target.top) < min(originY+half, target.bottom) {
			return true, nil
		}
	}
	return false, nil
}
