package content

import (
	"math"
	"testing"
	"time"
)

func TestResourceCostNativeIntegerOrderAndDistinctForce(t *testing.T) {
	base := ResourceCostModifiers{Prepared: true, MPConsumePercent: 150, MPReductionPercent: 33, CooldownReductionSeconds: 7}
	for _, tc := range []struct {
		name   string
		mp     int
		cd     time.Duration
		m      ResourceCostModifiers
		wantMP int
		wantCD time.Duration
	}{
		{"integer_amplification_then_reduction", 7, 10 * time.Second, base, 7, 3 * time.Second},
		{"native_not_java_rounding", 3, 0, base, 3, 0},
		{"over100_clamps_no_heal", 7, 0, ResourceCostModifiers{Prepared: true, MPConsumePercent: 150, MPReductionPercent: 150}, 0, 0},
		{"zero", 0, 0, base, 0, 0},
		{"cooldown_floor", 7, 6 * time.Second, base, 7, 0},
		{"infinity_keeps_cooldown", 7, 10 * time.Second, func() ResourceCostModifiers { m := base; m.Infinity = true; return m }(), 0, 3 * time.Second},
		{"force_not_mp", 7, 10 * time.Second, func() ResourceCostModifiers { m := base; m.DemonForce = true; m.Infinity = true; return m }(), 7, 3 * time.Second},
		{"boundless_only_force", 7, 10 * time.Second, func() ResourceCostModifiers { m := base; m.DemonForce = true; m.BoundlessRage = true; return m }(), 0, 3 * time.Second},
		{"boundless_not_infinity", 7, 10 * time.Second, func() ResourceCostModifiers { m := base; m.BoundlessRage = true; return m }(), 7, 3 * time.Second},
		{"full_reduction", 7, 10 * time.Second, ResourceCostModifiers{Prepared: true, MPConsumePercent: 150, MPReductionPercent: 100}, 0, 10 * time.Second},
		{"legacy_default", 7, time.Minute, ResourceCostModifiers{}, 7, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CalculateResourceCost(2121003, tc.mp, tc.cd, tc.m)
			if err != nil || got.MP != tc.wantMP || got.Cooldown != tc.wantCD {
				t.Fatal(got, err)
			}
		})
	}
	for _, m := range []ResourceCostModifiers{
		{Prepared: true, MPConsumePercent: 100, MPReductionPercent: -1},
		{Prepared: true, MPConsumePercent: -1},
		{Prepared: true, MPConsumePercent: 10001},
		{Prepared: true, MPConsumePercent: 100, CooldownReductionSeconds: -1},
		{Prepared: true, MPConsumePercent: 100, CooldownReductionSeconds: 86401},
	} {
		if got, err := CalculateResourceCost(2121003, 10, time.Minute, m); err == nil || got != (ResourceCost{}) {
			t.Fatal("invalid cost accepted", m, got, err)
		}
	}
	if _, err := CalculateResourceCost(2121003, math.MaxInt32, 0, ResourceCostModifiers{Prepared: true, MPConsumePercent: 10000}); err == nil {
		t.Fatal("overflow cost accepted")
	}
}

func TestResourceNativeMultiplierSkillPredicateModeAndTeleportPrecedence(t *testing.T) {
	m := ResourceCostModifiers{Prepared: true, MPConsumePercent: 150, MPReductionPercent: 33, TeleportExtraMP: 20, MechanicMode: true, MechanicMP: [3]int{21, 25, 25}}
	for _, tc := range []struct {
		id, mp, want int
		infinity     bool
	}{
		{2121003, 7, 7, false},  // Native list; floor(7*150/100)=10, then10-floor(3.3)=7.
		{2001002, 7, 5, false},  // A buff cast is outside the native MP amplification predicate.
		{2121008, 7, 5, false},  // Not every magic attack is in the list.
		{2101002, 7, 19, false}, // Teleport add20 before subtracting33%.
		{2101002, 7, 0, true},   // Infinity suppresses both the base and teleport extra.
		{35111004, 7, 15, true}, // Mech W replaces MP after Infinity, before reduction.
		{35121005, 7, 17, false},
		{35121013, 7, 17, false},
	} {
		m.Infinity = tc.infinity
		got, err := CalculateResourceCost(tc.id, tc.mp, 0, m)
		if err != nil || got.MP != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	m.MechanicMP[2] = 0
	if got, err := CalculateResourceCost(35121013, 100, 0, m); err != nil || got.MP != 0 {
		t.Fatal("missing learned tank W must remain zero", got, err)
	}
}
