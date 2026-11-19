package skills

import (
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// ComboDamagePercent is the physical damage bonus of the active combo orbs.
// v148 CalcDamage (0x71160B) multiplies by sub_6DB839, which takes the combo
// counter minus one as the orb count, caps it at x of Advanced Combo (else
// of Combo Attack) and returns 100 + orbs × (damR of Combo Attack + damR of
// Advanced Combo). Cygnus jobs use 11111001/11110005, the others
// 1111002/1120003.
func ComboDamagePercent(c domain.Character, now time.Time) int {
	combo, ok := ActiveStat(c, "COMBO", now)
	if !ok || combo < 2 {
		return 0
	}
	baseID, advancedID := 1111002, 1120003
	if c.Job/1000 == 1 {
		baseID, advancedID = 11111001, 11110005
	}
	base := learnedValues(c, baseID, now)
	if base == nil {
		return 0
	}
	perOrb, limit := base["damR"], base["x"]
	if advanced := learnedValues(c, advancedID, now); advanced != nil {
		perOrb += advanced["damR"]
		limit = advanced["x"]
	}
	return min(combo-1, max(limit, 0)) * perOrb
}

func learnedValues(c domain.Character, id int, now time.Time) map[string]int {
	level := EffectiveLevel(c, id, now)
	if level < 1 {
		return nil
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
	if err != nil || def.Disabled {
		return nil
	}
	values, err := def.Values(level)
	if err != nil {
		return nil
	}
	return values
}
