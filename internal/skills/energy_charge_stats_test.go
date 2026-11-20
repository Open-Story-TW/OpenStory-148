package skills_test

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestFullEnergyChargeCompetesWithAttackBuffs(t *testing.T) {
	now := time.Unix(100, 0)
	later := now.Add(time.Minute)
	gauge := func(v int) domain.SkillBuff {
		return domain.SkillBuff{ID: 15100004, Stat: "ENERGY_CHARGE", Value: v, ExpiresAt: later}
	}
	// 15100004 level 20: pad 20, acc 300, eva 300.
	tb := func(buffs ...domain.SkillBuff) domain.Character {
		return domain.Character{Job: 1511, Skills: []domain.SkillState{{ID: 15100004, Level: 20}}, SkillBuffs: buffs}
	}
	for _, tc := range []struct {
		name           string
		c              domain.Character
		watk, acc, eva int
	}{
		{"charging", tb(gauge(9_999)), 0, 0, 0},
		{"full", tb(gauge(10_000)), 20, 300, 300},
		{"full under a larger attack buff", tb(gauge(10_000), domain.SkillBuff{ID: 1, Stat: "WATK", Value: 30, ExpiresAt: later}), 30, 300, 300},
		{"full under a smaller attack buff", tb(gauge(10_000), domain.SkillBuff{ID: 1, Stat: "WATK", Value: 12, ExpiresAt: later}), 20, 300, 300},
		{"indie attack still adds", tb(gauge(10_000), domain.SkillBuff{ID: 2, Stat: "ENHANCED_WATK", Value: 5, ExpiresAt: later}), 25, 300, 300},
		{"expired gauge", tb(domain.SkillBuff{ID: 15100004, Stat: "ENERGY_CHARGE", Value: 10_000, ExpiresAt: now}), 0, 0, 0},
		{"not learned", domain.Character{Job: 1511, SkillBuffs: []domain.SkillBuff{gauge(10_000)}}, 0, 0, 0},
	} {
		b, _ := skills.BuffBonus(tc.c, now)
		if b.WATK != tc.watk || b.ACC != tc.acc || b.AVOID != tc.eva {
			t.Errorf("%s: watk %d acc %d eva %d, want %d %d %d", tc.name, b.WATK, b.ACC, b.AVOID, tc.watk, tc.acc, tc.eva)
		}
	}
	// A Buccaneer reads 5110001 (level 20: pad 20) from the same gauge.
	bucc := domain.Character{Job: 512, Skills: []domain.SkillState{{ID: 5110001, Level: 20}},
		SkillBuffs: []domain.SkillBuff{{ID: 5110001, Stat: "ENERGY_CHARGE", Value: 10_000, ExpiresAt: later}}}
	if b, _ := skills.BuffBonus(bucc, now); b.WATK != 20 {
		t.Errorf("Buccaneer full charge WATK %d, want 20", b.WATK)
	}
}
