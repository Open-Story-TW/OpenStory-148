package combat

import (
	"math"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

func TestMaxLineDamageFollowsTheLongDamageSwitch(t *testing.T) {
	previous := protocol.LongDamageCap()
	t.Cleanup(func() { protocol.SetLongDamageCap(previous) })
	protocol.SetLongDamageCap(0)
	if MaxLineDamage() != 999999 {
		t.Fatalf("off = %d", MaxLineDamage())
	}
	protocol.SetLongDamageCap(protocol.LongDamageMaxCap)
	if int64(MaxLineDamage()) != protocol.LongDamageMaxCap {
		t.Fatalf("on = %d", MaxLineDamage())
	}
}

func TestLineBoundsPassInt32OnlyWithLongDamage(t *testing.T) {
	previous := protocol.LongDamageCap()
	t.Cleanup(func() { protocol.SetLongDamageCap(previous) })
	c := DamageContext{
		Stats:  PlayerStats{STR: 30000, DEX: 30000, WATK: 29999, Level: 200, WeaponID: 1302000},
		Effect: Effect{Damage: 5000},
		Mob:    content.Mob{Level: 1},
	}
	protocol.SetLongDamageCap(0)
	if got := LineCeiling(c); got != 999999 {
		t.Fatalf("off = %d", got)
	}
	protocol.SetLongDamageCap(protocol.LongDamageMaxCap)
	floor, ceiling := LineBounds(c)
	if ceiling <= math.MaxInt32 || floor <= 0 || floor > ceiling {
		t.Fatalf("on = %d..%d", floor, ceiling)
	}
	if crit := CritCeiling(c, ceiling); crit <= ceiling {
		t.Fatalf("crit = %d", crit)
	}
}

func TestLineBoundsSaturateInsteadOfOverflowing(t *testing.T) {
	previous := protocol.LongDamageCap()
	t.Cleanup(func() { protocol.SetLongDamageCap(previous) })
	protocol.SetLongDamageCap(protocol.LongDamageMaxCap)
	c := DamageContext{
		Stats:  PlayerStats{STR: math.MaxInt32, DEX: math.MaxInt32, WATK: math.MaxInt32, Level: 200, WeaponID: 1302000, DamageRate: 100000},
		Effect: Effect{Damage: 1000000},
		Mob:    content.Mob{Level: 1},
	}
	if got := LineCeiling(c); int64(got) != protocol.LongDamageMaxCap {
		t.Fatalf("ceiling = %d", got)
	}
}

func TestMulDiv(t *testing.T) {
	for _, row := range []struct{ v, m, d, want int64 }{
		{10, 3, 2, 15},
		{math.MaxInt64, 2, 4, math.MaxInt64 / 2},
		{math.MaxInt64, 3, 1, math.MaxInt64},
		{0, 5, 1, 0},
		{5, 0, 1, 0},
		{-5, 2, 1, 0},
	} {
		if got := mulDiv(row.v, row.m, row.d); got != row.want {
			t.Fatalf("%d*%d/%d = %d", row.v, row.m, row.d, got)
		}
	}
}
