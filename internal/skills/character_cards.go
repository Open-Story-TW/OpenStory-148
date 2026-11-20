package skills

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func characterCardBonuses(root string, cards domain.CharacterCards) Bonus {
	var bonus Bonus
	for _, card := range cards {
		rank := card.Rank()
		if rank == 0 {
			continue
		}
		id, err := content.CharacterCardSkill(root, card.Job)
		if err != nil || id == 0 {
			continue
		}
		skill, err := content.SkillDefinition(root, id)
		if err != nil || skill.Disabled {
			continue
		}
		values, err := skill.Values(rank)
		if err != nil {
			continue
		}
		bonus.MaxHPPercent += values["mhpR"]
		bonus.MaxMPPercent += values["mmpR"]
		bonus.ACCPercent += values["accR"]
		bonus.AVOIDPercent += values["evaR"]
		bonus.Critical += values["cr"]
		bonus.CriticalDamageMin += values["criticaldamageMin"]
		bonus.CriticalDamage += values["criticaldamageMax"]
		bonus.Jump += values["psdJump"]
		bonus.Speed += values["psdSpeed"]
		bonus.ASR += values["asrR"]
	}
	return bonus
}
