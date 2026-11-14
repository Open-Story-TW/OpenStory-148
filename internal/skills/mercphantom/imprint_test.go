package mercphantom

import (
	"testing"

	"maple-go-server/internal/combat"
)

func TestImprintRaisesEveryCeiling(t *testing.T) {
	dc := combat.DamageContext{Statuses: combat.MobStatuses{combat.MobWeakness: {Stat: combat.MobWeakness, Value: 15, SkillID: 23111002}}}
	if got := imprintCeiling(dc, 1000); got != 1150 {
		t.Errorf("imprint ceiling %d", got)
	}
	dc.Statuses[combat.MobWeakness].SkillID = 1
	if got := imprintCeiling(dc, 1000); got != 1000 {
		t.Errorf("another weakness raised the ceiling to %d", got)
	}
	if got := imprintCeiling(combat.DamageContext{}, 1000); got != 1000 {
		t.Errorf("no status %d", got)
	}
}
