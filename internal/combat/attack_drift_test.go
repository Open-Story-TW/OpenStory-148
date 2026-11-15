package combat

import (
	"testing"

	"maple-go-server/internal/protocol"
)

// Slash Blast as the room saw it on 2026-11-15: the mob's last report put it
// 285 px ahead, the client had it inside the skill's 180 px. The reach is
// (180 + 29) * 2 = 418.
func TestAdmitsDoublesHorizontalReach(t *testing.T) {
	ctx := AttackContext{Kind: Melee, Effect: Effect{Range: 180}, Attack: protocol.Attack{X: 323, Y: 455}}
	for x, want := range map[int]bool{608: true, 323 + 418: true, 323 + 419: false, 323 - 418: true, 323 - 419: false} {
		if got := admits(ctx, TargetInfo{X: x, Y: 454, HalfWidth: 29, BodyHeight: 56}); got != want {
			t.Errorf("range: admits(x=%d) = %v, want %v", x, got, want)
		}
	}
	box := AttackContext{Kind: Melee, Effect: Effect{Box: Box{LT: Point{-180, -60}, RB: Point{0, 10}, Set: true}}, Attack: protocol.Attack{X: 323, Y: 455}}
	for x, want := range map[int]bool{323 + 418: true, 323 + 419: false, 323 - 418: true, 323 - 419: false} {
		if got := admits(box, TargetInfo{X: x, Y: 454, HalfWidth: 29, BodyHeight: 56}); got != want {
			t.Errorf("box: admits(x=%d) = %v, want %v", x, got, want)
		}
	}
}
