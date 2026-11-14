package content

import (
	"math"
	"testing"
)

func TestPotionRecoveryFlatHPBeforeRateAndCap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		r      Recovery
		v      PotionVitals
		bonus  int
		demon  bool
		hp, mp int
	}{
		{"flat then percent", Recovery{HP: 50, MP: 20, HPRate: 20, MPRate: 10}, PotionVitals{HP: 5, MP: 1, MaxHP: 500, MaxMP: 100}, 50, false, 180, 31},
		{"percentage unscaled", Recovery{HPRate: 20, MPRate: 10}, PotionVitals{HP: 5, MP: 1, MaxHP: 500, MaxMP: 100}, 200, false, 105, 11},
		{"integer truncate", Recovery{HP: 1}, PotionVitals{HP: 1, MaxHP: 100}, 50, false, 2, 0},
		{"sum once", Recovery{HP: 33}, PotionVitals{HP: 1, MaxHP: 500}, 80, false, 60, 0},
		{"final cap", Recovery{HP: 50}, PotionVitals{HP: 90, MaxHP: 100}, 50, false, 100, 0},
		{"expired maxima clamp", Recovery{HP: 50}, PotionVitals{HP: 200, MP: 80, MaxHP: 100, MaxMP: 30}, 50, false, 100, 30},
		{"force does not heal", Recovery{HP: 50, MP: 20, MPRate: 100}, PotionVitals{HP: 5, MP: 7, MaxHP: 100, MaxMP: 100}, 0, true, 55, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CalculatePotionRecovery(tc.r, tc.v, PotionRecoveryModifiers{FixedHPPercent: tc.bonus}, tc.demon)
			if err != nil || got.HP != tc.hp || got.MP != tc.mp {
				t.Fatal(got, err)
			}
		})
	}
	for _, change := range []func(*Recovery, *PotionVitals, *PotionRecoveryModifiers){
		func(_ *Recovery, v *PotionVitals, _ *PotionRecoveryModifiers) { v.HP = 0 },
		func(_ *Recovery, v *PotionVitals, _ *PotionRecoveryModifiers) { v.MP = -1 },
		func(r *Recovery, _ *PotionVitals, _ *PotionRecoveryModifiers) { r.HPRate = 101 },
		func(_ *Recovery, _ *PotionVitals, m *PotionRecoveryModifiers) { m.FixedHPPercent = -1 },
		func(r *Recovery, _ *PotionVitals, m *PotionRecoveryModifiers) {
			r.HP = math.MaxInt32
			m.FixedHPPercent = 50
		},
	} {
		r, v, m := Recovery{HP: 50}, PotionVitals{HP: 1, MaxHP: 100}, PotionRecoveryModifiers{}
		change(&r, &v, &m)
		if got, err := CalculatePotionRecovery(r, v, m, false); err == nil || got != (PotionVitals{}) {
			t.Fatal("invalid input produced recovery", got, err)
		}
	}
}
