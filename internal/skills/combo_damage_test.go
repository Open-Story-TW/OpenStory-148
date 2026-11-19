package skills_test

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func comboCharacter(job, combo int, learned ...domain.SkillState) domain.Character {
	now := time.Unix(100, 0)
	return domain.Character{Job: job, Skills: learned,
		SkillBuffs: []domain.SkillBuff{{ID: 11111001, Stat: "COMBO", Value: combo, ExpiresAt: now.Add(time.Minute)}}}
}

func TestComboOrbsMultiplyPhysicalDamage(t *testing.T) {
	now := time.Unix(100, 0)
	base := domain.SkillState{ID: 11111001, Level: 20}     // damR 5, x 5
	advanced := domain.SkillState{ID: 11110005, Level: 20} // damR 5, x 10
	for _, tc := range []struct {
		name string
		c    domain.Character
		want int
	}{
		{"no orbs", comboCharacter(1111, 1, base), 0},
		{"two orbs", comboCharacter(1111, 3, base), 10},
		{"capped at Combo Attack x", comboCharacter(1111, 9, base), 25},
		{"advanced adds its damR and cap", comboCharacter(1112, 9, base, advanced), 80},
		{"not learned", comboCharacter(1111, 3), 0},
	} {
		if got := skills.ComboDamagePercent(tc.c, now); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	hero := domain.Character{Job: 112, Skills: []domain.SkillState{{ID: 1111002, Level: 20}},
		SkillBuffs: []domain.SkillBuff{{ID: 1111002, Stat: "COMBO", Value: 3, ExpiresAt: now.Add(time.Minute)}}}
	if got := skills.ComboDamagePercent(hero, now); got != 10 {
		t.Errorf("Explorer combo: %d, want 10", got)
	}
}

func TestComboDamageRaisesOnlyThePhysicalCeiling(t *testing.T) {
	stats := combat.PlayerStats{Level: 200, STR: 1000, DEX: 100, WATK: 100, WeaponID: 1302000}
	physical := combat.DamageContext{Stats: stats, Effect: combat.Effect{Damage: 100}}
	_, plain := combat.LineBounds(physical)
	physical.Stats.ComboDamagePercent = 50
	if _, boosted := combat.LineBounds(physical); boosted < plain*3/2 || boosted > plain*3/2+1 {
		t.Fatalf("physical ceiling %d with 50%% combo, %d without", boosted, plain)
	}
	magic := combat.DamageContext{Stats: stats, Effect: combat.Effect{Damage: 100}, Magic: true}
	_, m0 := combat.LineBounds(magic)
	magic.Stats.ComboDamagePercent = 50
	if _, m1 := combat.LineBounds(magic); m1 != m0 {
		t.Fatalf("combo changed a magic ceiling: %d -> %d", m0, m1)
	}
}
