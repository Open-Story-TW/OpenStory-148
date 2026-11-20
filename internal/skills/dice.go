package skills

import (
	"fmt"
	"time"

	"maple-go-server/internal/domain"
)

// DiceCast stores the original outcome code and retains the first face for
// its transient animation, including a roll of one which creates no buff.
func DiceCast(c *CastContext, first, second int, upgraded bool) (CastPlan, error) {
	p := StandardCost(c)
	duration := time.Duration(c.Value("time")) * time.Second
	if duration < time.Second || first < 1 || first > 6 || second < 0 || second > 6 || (!upgraded && second != 0) {
		return p, fmt.Errorf("skill%d: invalid dice cast", c.Skill.ID)
	}
	p.Dice = domain.SkillDiceRoll{First: first, Second: second, Upgraded: upgraded}
	var value int
	switch {
	case first <= 1 && second <= 1:
		return p, nil
	case first == second:
		value = first * 100
	case first <= 1 || second <= 1:
		value = max(first, second)
	default:
		value = first*10 + second
	}
	p.Self = &Buff{Duration: duration, Stats: []StatValue{{Stat: "DICE_ROLL", Value: value}}}
	return p, nil
}
