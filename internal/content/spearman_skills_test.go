package content

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestSpearmanRetainsOnlyReviewedFirstWarriorAttacks(t *testing.T) {
	c := domain.Character{Job: 130, Level: 30, HP: 100, STR: 100, DEX: 30, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1432000, WATK: 30}}, Skills: []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}, {ID: 1301008, Level: 1}}}
	for _, id := range []int{0, 1001004, 1001005} {
		if _, err := PhysicalAttackProfileFor("", c, id, time.Now()); err != nil {
			t.Fatal("inherited attack rejected", id, err)
		}
	}
	for _, id := range []int{1301008, 2001004, 1101004} {
		if _, err := PhysicalAttackProfileFor("", c, id, time.Now()); err == nil {
			t.Fatal("unreviewed skill accepted", id)
		}
	}
	for _, job := range []int{110, 120, 200} {
		c.Job = job
		if _, err := PhysicalAttackProfileFor("", c, 1001004, time.Now()); err == nil {
			t.Fatal("unreviewed family accepted", job)
		}
	}
	c.Job = 130
	c.Level = 29
	if _, err := PhysicalAttackProfileFor("", c, 0, time.Now()); err == nil {
		t.Fatal("premature Spearman accepted")
	}
}
