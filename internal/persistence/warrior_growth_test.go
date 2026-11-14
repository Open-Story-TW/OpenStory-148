package persistence

import (
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestFirstWarriorLevelGrowthPreservesStatsAndAwardsSP(t *testing.T) {
	c := domain.Character{Job: 100, Level: 10, STR: 49, DEX: 4, INT: 4, LUK: 4, AP: 7, SP: 1, HP: 5, MP: 2, MaxHP: 300, MaxMP: 100}
	if err := applyQuestExperience(&c, content.ExperienceForLevel(10), "warrior-growth-known-operation"); err != nil {
		t.Fatal(err)
	}
	if c.Level != 11 || c.EXP != 0 || c.MaxHP < 348 || c.MaxHP > 352 || c.MaxMP < 104 || c.MaxMP > 106 || c.HP != c.MaxHP || c.MP != c.MaxMP || c.AP != 12 || c.SP != 4 || c.STR != 49 || c.DEX != 4 || c.INT != 4 || c.LUK != 4 {
		t.Fatalf("incorrect first-warrior growth: %+v", c)
	}
	if err := applyQuestExperience(&c, 1, "warrior-no-level-growth"); err != nil {
		t.Fatal(err)
	}
	if c.SP != 4 || c.AP != 12 {
		t.Fatal("non-level reward granted AP/SP")
	}
	c.Job = 900
	if err := applyQuestExperience(&c, 1, "unsupported-job-growth"); err == nil {
		t.Fatal("GM job silently used warrior growth")
	}
}
