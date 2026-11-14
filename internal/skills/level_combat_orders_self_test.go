package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestCombatOrdersDoesNotIncreaseItsOwnEffectiveLevel(t *testing.T) {
	now := time.Now()
	c := domain.Character{Job: 122,
		Skills:     []domain.SkillState{{ID: 1211011, Level: 19}, {ID: 1210001, Level: 10}},
		SkillBuffs: []domain.SkillBuff{{ID: 1211011, Level: 19, Stat: "COMBAT_ORDERS", ExpiresAt: now.Add(time.Minute)}},
	}
	for _, boost := range []int{0, 1, 2} {
		c.SkillBuffs[0].Value = boost
		if got := EffectiveLevel(c, 1211011, now); got != 19 {
			t.Fatal("native self-exclusion lost", boost, got)
		}
		if got := LinkedLevel(c, 1211011, 1211011, now); got != 19 {
			t.Fatal("same source/target wrapper changed self level", boost, got)
		}
		if got := EffectiveLevel(c, 1210001, now); got != 10+boost {
			t.Fatal("self-exclusion disabled other learned skills", boost, got)
		}
		if level, ok := CastLevel(c, 1211011, 1211011, 19, now); !ok || level != 19 {
			t.Fatal("native own level refused", boost, level, ok)
		}
		if level, ok := CastLevel(c, 1211011, 1211011, 20, now); ok || level != 0 {
			t.Fatal("self-derived extra level was accepted", boost, level, ok)
		}
	}
	// Original1211011 changes its actual party buff value across this boundary;
	// this is not only a label in a level list.
	def, err := content.SkillDefinition("", 1211011)
	if err != nil || def.MaxLevel != 20 || def.CombatOrders {
		t.Fatal("original self skill facts changed", def, err)
	}
	low, err := def.Values(19)
	if err != nil {
		t.Fatal(err)
	}
	high, err := def.Values(20)
	if err != nil || low["x"] != 1 || high["x"] != 2 {
		t.Fatal("original effect boundary changed", low, high, err)
	}
	c.Skills[0].ExpiresAt = now
	if got := EffectiveLevel(c, 1211011, now); got != 0 {
		t.Fatal("expired saved self skill is still effective", got)
	}
}
