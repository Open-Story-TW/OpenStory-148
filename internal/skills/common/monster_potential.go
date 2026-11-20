package common

import "maple-go-server/internal/skills"

// Equipment potential on-hit proc skills (Skill.wz/9000.img):
// v148 MDamage (0x708235) / PDamage (0x713448) evaluate CharacterData+0xA1E
// potential option skills; the server resolves both equipped ItemOption
// attackType lines (1000..1005) and learned 9000xxxx skills in
// skills.PlayerStatsChecked -> combat.PlanAttack.
func init() {
	skills.Register(skills.Handler{
		ID:   skills.MonsterPotentialDeath,
		Kind: skills.KindPassive | skills.KindSpecial,
	})
	for _, id := range []int{
		skills.MonsterPotentialStun,
		skills.MonsterPotentialSlow,
		skills.MonsterPotentialPoison,
		skills.MonsterPotentialDarkness,
		skills.MonsterPotentialSeal,
		skills.MonsterPotentialFreeze,
	} {
		skills.Register(skills.Handler{
			ID:   id,
			Kind: skills.KindPassive | skills.KindMobStatus,
		})
	}
}
