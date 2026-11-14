package combat

import (
	"testing"

	"maple-go-server/internal/protocol"
)

func TestAttackFacingNativeBitSelectsSpearBoxWithoutTighteningAdmission(t *testing.T) {
	// Actual WZ spear action13 swingP1 rectangle. Right-facing input below
	// is constructed; only the 0x800D left case is tied to the R5 capture.
	box := Box{LT: Point{-133, -120}, RB: Point{-51, 24}, Set: true}
	for _, tc := range []struct {
		display uint16
		lt, rb  Point
	}{
		{0x800D, Point{-137, 95}, Point{-55, 239}},
		{0x000D, Point{47, 95}, Point{129, 239}},
	} {
		a := protocol.Attack{Display: tc.display, X: -4, Y: 215}
		lt, rb := box.At(Point{int(a.X), int(a.Y)}, a.FacingLeft())
		if lt != tc.lt || rb != tc.rb {
			t.Fatalf("display%x selected %v..%v, want %v..%v", tc.display, lt, rb, tc.lt, tc.rb)
		}
		ctx := AttackContext{Kind: Melee, Attack: a, Effect: Effect{Box: box}}
		for _, x := range []int{-100, 100} {
			if !admits(ctx, TargetInfo{X: x, Y: 215}) {
				t.Fatalf("existing either-mirror admission changed display%x x%d", tc.display, x)
			}
		}
		if admits(ctx, TargetInfo{X: 500, Y: 215}) {
			t.Fatal("outside both mirrors admitted")
		}
	}
}
