package content

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestMPExpansionUsesWornMPAfterEquipmentAndPreservesIntrinsicWire(t *testing.T) {
	now := time.Now()
	c := domain.Character{Job: 200, VitalsModel: 1, BaseMaxHP: 100, BaseMaxMP: 99, MP: 30, Equipment: []domain.Equipment{{Slot: -11, MP: 21}, {Slot: -111, MP: 5}, {Slot: 1, MP: 900}}, Skills: []domain.SkillState{{ID: 2000006, Level: 1}}}
	hp, mp, err := DerivedVitals(c, now)
	if err != nil || hp != 100 || mp != 127 || c.MP != 30 || c.WireMaxMP() != 99 || c.BaseMaxMP != 99 {
		t.Fatalf("lv1 hp/mp %d/%d %+v %v", hp, mp, c, err)
	}
	c.Skills[0].Level = 10
	_, mp, err = DerivedVitals(c, now)
	if err != nil || mp != 150 {
		t.Fatalf("lv10 = %d %v", mp, err)
	}
	c.Skills[0].ExpiresAt = now
	_, mp, err = DerivedVitals(c, now)
	if err != nil || mp != 125 {
		t.Fatalf("expired passive = %d %v", mp, err)
	}
	c.Skills[0].ExpiresAt = time.Time{}
	c.BaseMaxMP = 99999
	defer domain.SetLongVitalsForTest(false)()
	_, mp, err = DerivedVitals(c, now)
	if err != nil || mp != 99999 {
		t.Fatalf("cap = %d %v", mp, err)
	}
}
func TestMPExpansionExactMageAncestryAndValidatedLevel(t *testing.T) {
	c := domain.Character{VitalsModel: 1, BaseMaxHP: 100, BaseMaxMP: 100, Skills: []domain.SkillState{{ID: 2000006, Level: 1}}}
	for _, job := range []int{200, 210, 211, 212, 220, 221, 222, 230, 231, 232} {
		c.Job = job
		_, mp, err := DerivedVitals(c, time.Now())
		if err != nil || mp != 102 {
			t.Fatalf("job%d = %d %v", job, mp, err)
		}
	}
	for _, job := range []int{0, 100, 201, 300, 400, 500, 1200} {
		c.Job = job
		_, mp, err := DerivedVitals(c, time.Now())
		if err != nil || mp != 100 {
			t.Fatalf("unrelated job%d = %d %v", job, mp, err)
		}
	}
	c.Job = 200
	c.Skills[0].Level = 11
	if _, _, err := DerivedVitals(c, time.Now()); err == nil {
		t.Fatal("above max learned level accepted")
	}
}
