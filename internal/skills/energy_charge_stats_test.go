package skills_test

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// energyValues are the loaded level-20 values of an Energy Charge skill:
// the stock data and the v148 WZ differ (v148 15100004 has pad 30, no eva).
func energyValues(t *testing.T, id int) map[string]int {
	t.Helper()
	def, err := content.ConfiguredSkillDefinition(id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(20)
	if err != nil {
		t.Fatal(err)
	}
	if v["pad"] < 1 {
		t.Fatalf("skill %d has no pad at level 20: %v", id, v)
	}
	return v
}

func TestFullEnergyChargeCompetesWithAttackBuffs(t *testing.T) {
	now := time.Unix(100, 0)
	later := now.Add(time.Minute)
	ec := energyValues(t, 15100004)
	pad, acc, eva := ec["pad"], ec["acc"], ec["eva"]
	gauge := func(v int) domain.SkillBuff {
		return domain.SkillBuff{ID: 15100004, Stat: "ENERGY_CHARGE", Value: v, ExpiresAt: later}
	}
	tb := func(buffs ...domain.SkillBuff) domain.Character {
		return domain.Character{Job: 1511, Skills: []domain.SkillState{{ID: 15100004, Level: 20}}, SkillBuffs: buffs}
	}
	watk := func(id, v int) domain.SkillBuff {
		return domain.SkillBuff{ID: id, Stat: "WATK", Value: v, ExpiresAt: later}
	}
	for _, tc := range []struct {
		name           string
		c              domain.Character
		watk, acc, eva int
	}{
		{"charging", tb(gauge(9_999)), 0, 0, 0},
		{"full", tb(gauge(10_000)), pad, acc, eva},
		{"full under a larger attack buff", tb(gauge(10_000), watk(1, pad+10)), pad + 10, acc, eva},
		{"full under a smaller attack buff", tb(gauge(10_000), watk(1, pad-1)), pad, acc, eva},
		{"indie attack still adds", tb(gauge(10_000), domain.SkillBuff{ID: 2, Stat: "ENHANCED_WATK", Value: 5, ExpiresAt: later}), pad + 5, acc, eva},
		{"expired gauge", tb(domain.SkillBuff{ID: 15100004, Stat: "ENERGY_CHARGE", Value: 10_000, ExpiresAt: now}), 0, 0, 0},
		{"not learned", domain.Character{Job: 1511, SkillBuffs: []domain.SkillBuff{gauge(10_000)}}, 0, 0, 0},
	} {
		b, _ := skills.BuffBonus(tc.c, now)
		if b.WATK != tc.watk || b.ACC != tc.acc || b.AVOID != tc.eva {
			t.Errorf("%s: watk %d acc %d eva %d, want %d %d %d", tc.name, b.WATK, b.ACC, b.AVOID, tc.watk, tc.acc, tc.eva)
		}
	}
	// A Buccaneer reads 5110001 from the same gauge.
	bucc := domain.Character{Job: 512, Skills: []domain.SkillState{{ID: 5110001, Level: 20}},
		SkillBuffs: []domain.SkillBuff{{ID: 5110001, Stat: "ENERGY_CHARGE", Value: 10_000, ExpiresAt: later}}}
	if b, _ := skills.BuffBonus(bucc, now); b.WATK != energyValues(t, 5110001)["pad"] {
		t.Errorf("Buccaneer full charge WATK %d, want %d", b.WATK, energyValues(t, 5110001)["pad"])
	}
}
