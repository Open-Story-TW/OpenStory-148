package persistence

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestFirstWarriorFinalStatsPreserveAPAndUseLegacyHealthRange(t *testing.T) {
	c := domain.Character{Job: 0, Level: 10, STR: 57, DEX: 5, INT: 4, LUK: 4, AP: 3, HP: 50, MaxHP: 182, MP: 10, MaxMP: 104}
	result, err := firstWarriorStats(c, "first-warrior-server-operation")
	if err != nil {
		t.Fatal(err)
	}
	if result.Job != 100 || result.STR != 4 || result.DEX != 4 || result.INT != 4 || result.LUK != 4 || result.AP != 57 || result.SP != 1 || result.MaxHP < 382 || result.MaxHP > 432 || result.HP != result.MaxHP || result.MP != result.MaxMP {
		t.Fatalf("wrong legacy advancement totals: %+v", result)
	}
	replayed, err := firstWarriorStats(c, "first-warrior-server-operation")
	if err != nil || replayed.MaxHP != result.MaxHP {
		t.Fatal("operation rerolled the health bonus")
	}
	if c.Job != 0 || c.STR != 57 || c.AP != 3 {
		t.Fatal("detached input mutated")
	}
}

func TestFirstWarriorRejectsWrongJobLevelDeadAndOverflow(t *testing.T) {
	base := domain.Character{Job: 0, Level: 10, STR: 57, DEX: 5, INT: 4, LUK: 4, HP: 50, MaxHP: 182, MP: 10, MaxMP: 104}
	for name, modify := range map[string]func(*domain.Character){
		"low level":    func(c *domain.Character) { c.Level = 9 },
		"existing job": func(c *domain.Character) { c.Job = 100 },
		"dead":         func(c *domain.Character) { c.HP = 0 },
		"overflow AP":  func(c *domain.Character) { c.AP = domain.MaxStat() },
		"overflow SP":  func(c *domain.Character) { c.SP = 65535 },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			modify(&c)
			if _, err := firstWarriorStats(c, "first-warrior-server-operation"); err == nil {
				t.Fatal("invalid advancement accepted")
			}
		})
	}
}
