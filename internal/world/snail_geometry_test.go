package world

import (
	"maple-go-server/internal/content"
	"math"
	"testing"
)

func snailTestBounds(r content.MobBodyRect) content.MobBodyBounds {
	return content.MobBodyBounds{Kind: "static_frame_envelope_both_facings", FrameCount: 1, Envelope: r}
}
func TestSnailGeometryAcceptsBodyIntersectionBeyondOld220AndRespectsEndpoint(t *testing.T) {
	body := snailTestBounds(content.MobBodyRect{Left: -34, Top: -54, Right: 34, Bottom: 0})
	for _, q := range []struct {
		x          int
		left, want bool
	}{{280, false, true}, {333, false, true}, {334, false, false}, {-280, true, true}, {-333, true, true}, {-334, true, false}, {-100, false, false}, {100, true, false}} {
		got, e := snailGeometryAdmits(snailPosition{}, q.left, body, snailPosition{q.x, 0}, nil)
		if e != nil || got != q.want {
			t.Fatal(q, got, e)
		}
	}
}
func TestSnailGeometryNativeMuzzleGapFeetOffsetAndQuantizedHalfHeight(t *testing.T) {
	b := snailTestBounds(content.MobBodyRect{Left: -1, Top: -1, Right: 1, Bottom: 1})
	for _, q := range []struct {
		x, y int
		want bool
	}{{49, -28, false}, {50, -28, true}, {51, -40, true}, {51, -41, false}, {71, -45, true}, {71, -46, false}, {295, -100, true}, {295, -101, false}, {295, 44, true}, {295, 45, false}, {295, 0, true}} {
		got, e := snailGeometryAdmits(snailPosition{}, false, b, snailPosition{q.x, q.y}, nil)
		if e != nil || got != q.want {
			t.Fatal(q, got, e)
		}
	}
	got, e := snailGeometryAdmits(snailPosition{X: 100, Y: 200}, false, b, snailPosition{X: 151, Y: 160}, nil)
	if e != nil || !got {
		t.Fatal("ray failed to translate feetY-28", got, e)
	}
}
func TestSnailGeometryExplicitPriorPositionUsesNativeUnionWithoutPadding(t *testing.T) {
	b := snailTestBounds(content.MobBodyRect{Left: -34, Top: -54, Right: 34, Bottom: 0})
	now := snailPosition{400, 0}
	old := snailPosition{280, 0}
	without, e := snailGeometryAdmits(snailPosition{}, false, b, now, nil)
	with, e2 := snailGeometryAdmits(snailPosition{}, false, b, now, &old)
	if e != nil || e2 != nil || without || !with {
		t.Fatal(without, with, e, e2)
	}
	old = snailPosition{350, 0}
	with, e = snailGeometryAdmits(snailPosition{}, false, b, now, &old)
	if e != nil || with {
		t.Fatal("invented padding beyond provided history", with, e)
	}
}
func TestSnailGeometryRejectsUnknownOrOverflowingInputs(t *testing.T) {
	b := snailTestBounds(content.MobBodyRect{Left: -34, Top: -54, Right: 34, Bottom: 0})
	for _, bad := range []content.MobBodyBounds{{}, snailTestBounds(content.MobBodyRect{}), snailTestBounds(content.MobBodyRect{Left: -5000, Top: -10, Right: 1, Bottom: 0})} {
		if got, e := snailGeometryAdmits(snailPosition{}, false, bad, snailPosition{100, 0}, nil); e == nil || got {
			t.Fatal(got, e)
		}
	}
	if got, e := snailGeometryAdmits(snailPosition{X: math.MaxInt64}, false, b, snailPosition{100, 0}, nil); e == nil || got {
		t.Fatal(got, e)
	}
	for _, p := range []snailPosition{{X: math.MaxInt32}, {Y: math.MinInt32}} {
		if got, e := snailGeometryAdmits(p, false, b, snailPosition{100, 0}, nil); e == nil || got {
			t.Fatal("native coordinate arithmetic overflow", got, e)
		}
	}
	if got, e := snailGeometryAdmits(snailPosition{}, false, b, snailPosition{X: math.MaxInt32}, nil); e == nil || got {
		t.Fatal("body offset overflow", got, e)
	}
}
