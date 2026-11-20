package skills

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// guildBonuses evaluates the purchased guild ranks independently of the
// character's learned skills. v148 checks rank and expiry in its passive
// rebuild at 0x91667E; weekly supplies have a separate delivery.
func guildBonuses(root string, c domain.Character, now time.Time) Bonus {
	if c.Guild.ID == 0 {
		return Bonus{}
	}
	var bonus Bonus
	for _, rank := range c.GuildSkills {
		if rank.SkillID < 91000000 || rank.SkillID > 91000005 || rank.Level < 1 || rank.ExpiresAt <= now.UnixMilli() {
			continue
		}
		def, err := content.SkillDefinition(root, rank.SkillID)
		if err != nil || def.Disabled {
			continue
		}
		values, err := def.Values(rank.Level)
		if err != nil {
			continue
		}
		switch rank.SkillID {
		case 91000000:
			bonus.ACCPercent += values["ar"]
			bonus.GuildAVOIDPercent += values["er"]
		case 91000001:
			bonus.EXP += values["expR"]
		case 91000002:
			bonus.GuildWDEFPercent += values["pddR"]
			bonus.GuildMDEFPercent += values["mddR"]
		case 91000003:
			bonus.WATK += values["padX"]
			bonus.MATK += values["madX"]
		case 91000004:
			bonus.Speed += values["psdSpeed"]
			bonus.Jump += values["psdJump"]
		case 91000005:
			bonus.Critical += values["cr"]
		}
	}
	return bonus
}
