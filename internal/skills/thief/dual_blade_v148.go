package thief

import "maple-go-server/internal/skills"

func init() {
	skills.Register(skills.Handler{ID: 4301003, Kind: skills.KindBuff | skills.KindPassive, Cast: skills.StandardBuff, Passive: skills.MovementPassive}) // 自我速度激發
	skills.Register(skills.Handler{ID: 4301004, Kind: skills.KindAttack, Attack: melee()})                                                               // 雙刃旋
	skills.Register(skills.Handler{ID: 4310006, Kind: skills.KindPassive, Passive: physicalTraining})
	skills.Register(skills.Handler{ID: 4311005, Kind: skills.KindBuff, Cast: skills.StandardBuff}) // 輪迴
	skills.Register(skills.Handler{ID: 4311009, Kind: skills.KindBuff, Cast: booster})             // 神速雙刀
	skills.Register(skills.Handler{ID: 4320005, Kind: skills.KindPassive | skills.KindMobStatus})  // 飛毒殺, applied by venom
	skills.Register(skills.Handler{ID: 4321006, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(stun))})
	skills.Register(skills.Handler{ID: 4330008, Kind: skills.KindPassive, Passive: shadowResistance})
	// 暗影迴避: the WZ er dodge chance. The 1 s indiePad and the critical
	// follow-up after a dodge are not applied yet.
	skills.Register(skills.Handler{ID: 4330009, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{AvoidRate: c.Values["er"]}
	}})
	skills.Register(skills.Handler{ID: 4340013, Kind: skills.KindPassive, Passive: bladeExpertise})
	skills.Register(skills.Handler{ID: 4341011, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(prepare(suddenRaid))})
}

// bladeExpertise adds the v148 WZ mastery, attack, hit and evasion bonuses
// with a dagger or katara equipped.
func bladeExpertise(c *skills.PassiveContext) skills.Bonus {
	if weaponType(c.Character) == weaponKatara {
		return expertMastery(weaponKatara)(c)
	}
	return expertMastery(weaponDagger)(c)
}
