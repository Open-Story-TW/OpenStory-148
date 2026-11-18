package pirate

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// Weapon types (item id / 10000) the mastery passives require.
const (
	knuckle = 148
	gun     = 149
	cannon  = 153
)

func init() {
	register := func(id int, f func(*skills.PassiveContext) skills.Bonus) {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
	}
	register(5000000, bulletTime)
	register(5010003, cannonUpgrade)
	register(5100001, mastery(knuckle, "x"))
	register(5200000, mastery(gun, "x"))
	register(5300005, mastery(cannon, "accX"))
	register(5100008, critical("cr"))   // 衝壓暴擊
	register(5110000, critical("prop")) // 致命暗襲: Java uses prop as the rate
	register(5200007, critical("cr"))   // 會心一擊-改
	register(5300004, critical("cr"))   // 烈火暴擊
	register(5100009, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{MaxHPPercent: c.Values["mhpR"]} })
	register(5310006, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{WATK: c.Values["padX"]} }) // 強化加農砲
	register(5310007, ultimateState)
	register(5110008, func(c *skills.PassiveContext) skills.Bonus { // 精通近身戰
		return skills.Bonus{SkillDamage: skillDamage(5101002, c.Values["x"], 5101003, c.Values["y"], 5101004, c.Values["z"])}
	})
	register(5220001, func(c *skills.PassiveContext) skills.Bonus { // 瞬‧冰火連擊
		return skills.Bonus{SkillDamage: skillDamage(5211004, c.Values["damage"], 5211005, c.Values["damage"])}
	})
	register(5311001, func(c *skills.PassiveContext) skills.Bonus { // 猴子的強力炸彈
		return skills.Bonus{SkillDamage: skillDamage(5301001, c.Values["damR"])}
	})
	register(5300008, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
	}) // 百烈訓練
	register(5320009, func(c *skills.PassiveContext) skills.Bonus { // 炎熱加農砲 (Java reads it as 5321009)
		return skills.Bonus{DamagePercent: c.Values["damR"], BossDamagePercent: c.Values["damR"], IgnoreDefense: c.Values["ignoreMobpdpR"]}
	})
	// 猴子衝擊 is a proc, not a stat bonus.
	skills.Register(skills.Handler{ID: 5310008, Kind: skills.KindPassive})
}

// weaponType is the equipped weapon's item id / 10000, 0 when bare-handed.
func weaponType(c domain.Character) int {
	for _, e := range c.Equipment {
		if e.Slot == -11 {
			return e.ItemID / 10000
		}
	}
	return 0
}

// bulletTime (極限迴避 5000000): +x accuracy and +y avoidability. Java Legacy
// does not apply it; this follows the WZ values.
func bulletTime(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{ACC: c.Values["x"], AVOID: c.Values["y"]}
}

// cannonUpgrade adds the skill's weapon attack and both defenses.
func cannonUpgrade(c *skills.PassiveContext) skills.Bonus {
	if j := c.Character.Job; j != 501 && j/10 != 53 {
		return skills.Bonus{}
	}
	return skills.Bonus{WATK: c.Values["padX"], WDEF: c.Values["pddX"], MDEF: c.Values["mddX"]}
}

// mastery is Java PlayerStats' weapon mastery: with the matching weapon, the
// skill's mastery and accuracy key. Cannon Mastery stores its accuracy in accX
// (Java reads x, which is zero for it).
func mastery(weapon int, accKey string) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		if weaponType(c.Character) != weapon {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.Values["mastery"], ACC: c.Values[accKey]}
	}
}

// critical adds the critical rate key and criticaldamageMin (Java
// passive_sharpeye_rate and passive_sharpeye_min_percent).
func critical(rateKey string) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{Critical: c.Values[rateKey], CriticalDamageMin: c.Values["criticaldamageMin"]}
	}
}

// ultimateState adds max HP, both defenses, and status resistance.
func ultimateState(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{
		MaxHPPercent: c.Values["mhpR"], ASR: c.Values["asrR"],
		WDEFPercent: c.Values["pddR"], MDEFPercent: c.Values["mddR"],
	}
}

// skillDamage packs (skill, percent) pairs into Bonus.SkillDamage.
func skillDamage(pairs ...int) (out [4]skills.SkillDamage) {
	for i := 0; i+1 < len(pairs); i += 2 {
		out[i/2] = skills.SkillDamage{Skill: pairs[i], Percent: pairs[i+1]}
	}
	return out
}
