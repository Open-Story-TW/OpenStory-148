package all_test

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

func TestMasteryCountsOnlyOnTheJobLine(t *testing.T) {
	equip := []domain.Equipment{{Slot: -11, ItemID: 1302000}}
	learned := []domain.SkillState{{ID: 1100000, Level: 20}, {ID: 1100009, Level: 10}} // Weapon Mastery, Physical Training.
	now := time.Now()
	hero := skills.Bonuses(domain.Character{Job: 112, Level: 200, Skills: learned, Equipment: equip}, now)
	if hero.Mastery != 50 || hero.STR != 30 {
		t.Fatalf("a Hero keeps its line's passives: %+v", hero)
	}
	mage := skills.Bonuses(domain.Character{Job: 212, Level: 200, Skills: learned, Equipment: equip}, now)
	if mage.Mastery != 0 {
		t.Fatalf("another line's mastery must not raise the floor: %d", mage.Mastery)
	}
	if mage.STR != 30 {
		t.Fatalf("the client still counts other passives: STR %d", mage.STR)
	}
}
