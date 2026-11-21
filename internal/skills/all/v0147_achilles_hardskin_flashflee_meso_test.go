package all_test

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

func TestAchillesAndHardSkinDamageReduction(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name  string
		job   int
		skill int
		level int
	}{
		{"paladin_achilles", 122, 1220005, 30},
		{"demon_hard_skin", 3112, 31120009, 30},
	} {
		ch := domain.Character{
			Job: tc.job, HP: 1000, MaxHP: 1000,
			Skills: []domain.SkillState{{ID: tc.skill, Level: tc.level}},
		}
		if b := skills.Bonuses(ch, now); b.WDEFPercent != 20 || b.DamageReduce != 20 {
			t.Fatalf("%s: got WDEFPercent=%d DamageReduce=%d, want 20/20", tc.name, b.WDEFPercent, b.DamageReduce)
		}
		hp, _, _ := skills.TakenDamageWithMesos(ch, 1000, now)
		if hp != 800 {
			t.Fatalf("%s: taken hp=%d, want 800", tc.name, hp)
		}
	}
}

func TestFlashAndFleeAndMesoUpBonuses(t *testing.T) {
	now := time.Now()
	ph := domain.Character{
		Job:    2411,
		Skills: []domain.SkillState{{ID: 24110004, Level: 20}},
	}
	if b := skills.Bonuses(ph, now); b.AvoidRate != 30 {
		t.Fatalf("flash and flee 24110004: got AvoidRate=%d, want 30", b.AvoidRate)
	}
	mu := domain.Character{
		SkillBuffs: []domain.SkillBuff{{ID: 4111001, Level: 20, Stat: "MESOUP", Value: 150, ExpiresAt: now.Add(time.Minute)}},
	}
	if b, _ := skills.BuffBonus(mu, now); b.MesoRate != 50 {
		t.Fatalf("meso up 4111001: got MesoRate=%d, want 50", b.MesoRate)
	}
}
