package content

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestDerivedVitalsCountsWornOnceAndAppliesPassiveAfterEquipment(t *testing.T) {
	c := domain.Character{VitalsModel: 1, BaseMaxHP: 100, BaseMaxMP: 20, Job: 100, Equipment: []domain.Equipment{{Slot: -11, HP: 20, MP: 5}, {Slot: -111, HP: 5, MP: 2}, {Slot: 1, HP: 9999, MP: 9999}}, Skills: []domain.SkillState{{ID: 1000006, Level: 1}}}
	hp, mp, err := DerivedVitals(c, time.Now())
	if err != nil || hp != 127 || mp != 27 {
		t.Fatalf("derived hp/mp=%d/%d %v", hp, mp, err)
	}
	if c.BaseMaxHP != 100 || c.BaseMaxMP != 20 || c.WireMaxHP() != 100 || c.WireMaxMP() != 20 {
		t.Fatal("derived or wire stats altered intrinsic values")
	}
	c.Skills[0].ExpiresAt = time.Now().Add(-time.Second)
	hp, _, err = DerivedVitals(c, time.Now())
	if err != nil || hp != 125 {
		t.Fatal("expired passive affected effectiveHP")
	}
	c.BaseMaxHP = 99999
	restore := domain.SetLongVitalsForTest(false)
	hp, _, err = DerivedVitals(c, time.Now())
	restore()
	if err != nil || hp != 99999 {
		t.Fatal("effectiveHP cap not applied")
	}
	c.VitalsModel = 0
	c.MaxHP = 90
	if _, _, err = DerivedVitals(c, time.Now()); err == nil || c.WireMaxHP() != 90 {
		t.Fatal("ambiguous base was inferred again")
	}
}
