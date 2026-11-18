package combat

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestZenIntrinsicProjectileStatsByJobAndWeapon(t *testing.T) {
	for _, want := range []struct{ job, weapon, equipmentPAD, PAD, roundedDamage int }{
		{508, 1492004, 31, 42, 800},
		{509, 1492004, 31, 31, 591},
		{570, 1492004, 31, 45, 857},
		{571, 1492004, 31, 48, 914},
		{572, 1492004, 31, 51, 972},
		{522, 1492004, 31, 31, 591},
		{572, 1492000, 13, 33, 629},
		{572, 1482000, 18, 38, 375},
		{522, 1482000, 18, 18, 177},
	} {
		c := domain.Character{Job: want.job, STR: 70, DEX: 300,
			Equipment: []domain.Equipment{{ItemID: want.weapon, Slot: -11, WATK: want.equipmentPAD}}}
		got, err := BaseStats(c, time.Time{})
		damage := int((maxBaseScaled(got) + 5000) / 10000)
		if err != nil || got.WATK != want.PAD || damage != want.roundedDamage {
			t.Fatalf("job %d, weapon %d: PAD %d, damage %d, error %v, want %d and %d",
				want.job, want.weapon, got.WATK, damage, err, want.PAD, want.roundedDamage)
		}
	}
}
