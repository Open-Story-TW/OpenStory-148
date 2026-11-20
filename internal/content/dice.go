package content

import (
	"time"

	"maple-go-server/internal/domain"
)

// DiceBonuses is the subset of the original 22-integer Dice outcome that
// Lucky Dice populates. The v148 tooltip reads these six values directly.
type DiceBonuses struct {
	HPPercent, MPPercent, Critical, DefensePercent, DamagePercent, ExperiencePercent int
}

// DiceBuffBonuses decodes the stored single, mixed, or doubled outcome. The
// buff's stored skill level, not its owner's current skill rank, selects WZ.
func DiceBuffBonuses(row domain.SkillBuff) DiceBonuses {
	if row.Stat != "DICE_ROLL" || row.Level < 1 {
		return DiceBonuses{}
	}
	def, err := ConfiguredSkillDefinition(row.ID)
	if err != nil || def.Disabled {
		return DiceBonuses{}
	}
	values, err := def.Values(row.Level)
	if err != nil {
		return DiceBonuses{}
	}
	first, second := row.Value/10, row.Value%10
	mixed := first >= 2 && first <= 6 && second >= 2 && second <= 6 && first != second
	bonus := func(face int, key string) int {
		value := max(0, values[key])
		switch {
		case row.Value == face*100 && value > 0:
			return value + 10
		case row.Value == face, mixed && (first == face || second == face):
			return value
		default:
			return 0
		}
	}
	return DiceBonuses{
		HPPercent: bonus(3, "mhpR"), MPPercent: bonus(3, "mmpR"), Critical: bonus(4, "cr"),
		DefensePercent: bonus(2, "pddR"), DamagePercent: bonus(5, "damR"), ExperiencePercent: bonus(6, "expR"),
	}
}

// ActiveDiceBonuses returns the authoritative result of the currently active
// roll. All dice effects share the same exclusive temporary stat.
func ActiveDiceBonuses(c domain.Character, now time.Time) DiceBonuses {
	for _, row := range c.SkillBuffs {
		if row.Stat == "DICE_ROLL" && row.ExpiresAt.After(now) {
			return DiceBuffBonuses(row)
		}
	}
	return DiceBonuses{}
}
