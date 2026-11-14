package content

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestDerivedVitalsFollowTheLongVitalsCeiling(t *testing.T) {
	c := domain.Character{VitalsModel: 1, BaseMaxHP: 5_000_000, BaseMaxMP: 120_000, Job: 100, Equipment: []domain.Equipment{{Slot: -5, HP: 30_000, MP: 7}}}
	defer domain.SetLongVitalsForTest(true)()
	hp, mp, err := DerivedVitals(c, time.Now())
	if err != nil || hp != 5_030_000 || mp != 120_007 {
		t.Fatalf("long vitals hp/mp=%d/%d %v", hp, mp, err)
	}
	c.BaseMaxHP = domain.VitalCeiling
	if hp, _, err = DerivedVitals(c, time.Now()); err != nil || hp != domain.VitalCeiling {
		t.Fatalf("ceiling hp=%d %v", hp, err)
	}
	c.BaseMaxHP = domain.VitalCeiling + 1
	if _, _, err = DerivedVitals(c, time.Now()); err == nil {
		t.Fatal("a base above the database ceiling was accepted")
	}
	// Switched off, a base above 99,999 stays valid and only the effective
	// maxima fall back to the native cap.
	domain.SetLongVitalsForTest(false)
	c.BaseMaxHP = 5_000_000
	if hp, mp, err = DerivedVitals(c, time.Now()); err != nil || hp != 99_999 || mp != 99_999 {
		t.Fatalf("native cap hp/mp=%d/%d %v", hp, mp, err)
	}
}

func TestPotionRecoveryAcceptsLongVitals(t *testing.T) {
	defer domain.SetLongVitalsForTest(true)()
	v, err := CalculatePotionRecovery(Recovery{HPRate: 100}, PotionVitals{HP: 1, MP: 0, MaxHP: 8_000_000, MaxMP: 300_000}, PotionRecoveryModifiers{}, false)
	if err != nil || v.HP != 8_000_000 || v.MP != 0 {
		t.Fatalf("recovery %+v %v", v, err)
	}
	domain.SetLongVitalsForTest(false)
	if _, err = CalculatePotionRecovery(Recovery{HPRate: 100}, PotionVitals{HP: 1, MaxHP: 8_000_000}, PotionRecoveryModifiers{}, false); err == nil {
		t.Fatal("max HP above the native cap accepted with long vitals off")
	}
}
