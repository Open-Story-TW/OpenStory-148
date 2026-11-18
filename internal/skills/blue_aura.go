package skills

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

const (
	BlueAuraSkill         = 32111012
	AdvancedBlueAuraSkill = 32110000
)

func IsBlueAuraSkill(id int) bool {
	return id == BlueAuraSkill || id == AdvancedBlueAuraSkill
}

func IsBlueAuraRow(row domain.SkillBuff) bool {
	return row.Stat == "BLUE_AURA" && IsBlueAuraSkill(row.ID)
}

// MatchesPartyBuff treats the two Blue Aura effect IDs as one cancellable
// toggle. The native client normalizes the upgraded icon's cancel to the
// basic skill in 0x92F042.
func MatchesPartyBuff(id int, row domain.SkillBuff) bool {
	return row.ID == id || IsBlueAuraSkill(id) && IsBlueAuraRow(row)
}

func ActiveBlueAura(c domain.Character, now time.Time) (domain.SkillBuff, bool) {
	for _, row := range c.SkillBuffs {
		if IsBlueAuraRow(row) && row.ExpiresAt.After(now) {
			return row, true
		}
	}
	return domain.SkillBuff{}, false
}

func PartyBuffSource(plan CastPlan, id, level int) (int, int) {
	if plan.ApplyAs != 0 {
		id = plan.ApplyAs
		if plan.ApplyLevel > 0 {
			level = plan.ApplyLevel
		}
	}
	return id, level
}

func PartyToggleActive(plan CastPlan, c domain.Character, id int, now time.Time) bool {
	if !plan.Toggle {
		return false
	}
	if id == BlueAuraSkill {
		row, ok := ActiveBlueAura(c, now)
		return ok && (row.SourceCharacterID == 0 || row.SourceCharacterID == c.ID)
	}
	applied, _ := PartyBuffSource(plan, id, 0)
	return ActiveBuff(c, applied, now)
}

// BlueAuraContactShare uses the raw mob hit used by the native client at
// 0x919620, independently of its later HP and MP deductions. The caster
// absorbs y percent of the distributed portion according to the WZ tooltip.
func BlueAuraContactShare(root string, row domain.SkillBuff, raw, hpLoss int) (recipient, caster int) {
	if !IsBlueAuraRow(row) || raw <= 0 || hpLoss <= 0 {
		return hpLoss, 0
	}
	def, err := content.SkillDefinition(root, row.ID)
	if err != nil {
		return hpLoss, 0
	}
	values, err := def.Values(row.Value)
	if err != nil || values["x"] < 0 || values["x"] > 100 || values["y"] < 0 || values["y"] > 100 {
		return hpLoss, 0
	}
	shared := min(hpLoss, raw*values["x"]/100)
	return hpLoss - shared, shared - shared*values["y"]/100
}
