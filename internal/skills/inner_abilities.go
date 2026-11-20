package skills

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func innerAbilityBonuses(root string, character domain.Character) Bonus {
	var bonus Bonus
	if root == "" {
		return bonus
	}
	for _, line := range character.Abilities[:character.InnerAbilitySlots()] {
		if !line.Valid() {
			continue
		}
		skill, err := content.SkillDefinition(root, line.ID)
		if err != nil || skill.Disabled {
			continue
		}
		values, err := skill.Values(line.Level)
		if err != nil {
			continue
		}
		bonus.STR += values["strFX"]
		bonus.DEX += values["dexFX"]
		bonus.INT += values["intFX"]
		bonus.LUK += values["lukFX"]
		bonus.ACC += values["accX"]
		bonus.AVOID += values["evaX"]
		bonus.WDEF += values["pddX"]
		bonus.MDEF += values["mddX"]
		bonus.MaxHP += values["mhpX"]
		bonus.MaxMP += values["mmpX"]
		bonus.MaxHPPercent += values["mhpR"]
		bonus.MaxMPPercent += values["mmpR"]
		bonus.Speed += values["psdSpeed"]
		bonus.Jump += values["psdJump"]
		bonus.WATK += values["padX"]
		bonus.MATK += values["madX"]
		bonus.Critical += values["cr"]
		bonus.ACCPercent += values["accR"]
		bonus.BossDamagePercent += values["bdR"]
	}
	return bonus
}
