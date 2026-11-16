package thief

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// The thief skills the v148 client has and v145 did not. Values come from the
// built-in v148 books; nothing here has a Java reference, so each rule says
// what it takes from the WZ data. Skills marked "cost only" pay their cost and
// show the cast, and their effect is not applied yet.
func init() {
	passive := func(id int, f func(*skills.PassiveContext) skills.Bonus) {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
	}
	// Either family: the client picks close range or ranged by the weapon.
	either := func(opts ...func(*combat.AttackSpec)) *combat.AttackSpec {
		return attack(combat.Melee|combat.Shoot, append(opts, thiefAttack)...)
	}
	strike := func(id int, spec *combat.AttackSpec) {
		kind := skills.KindAttack
		if spec.OnHit != nil {
			kind |= skills.KindMobStatus
		}
		skills.Register(skills.Handler{ID: id, Kind: kind, Attack: spec})
	}

	// 盜賊 400
	passive(4000012, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{AvoidRate: c.Values["er"]} }) // 迴避: er
	skills.Register(skills.Handler{ID: 4001005, Kind: skills.KindPartyBuff, Cast: skills.StandardPartyBuff})         // 速度激發: speed, jump
	skills.Register(skills.Handler{ID: 4001011, Kind: skills.KindSpecial, Cast: skills.CostOnly})                    // 二段跳
	strike(4001013, either(pickpocketOn))                                                                            // 狂刃刺擊

	// 刺客 410, 暗殺者 411, 夜使者 412
	passive(4100007, physicalTraining)
	strike(4101008, either())                                                                     // 爆破鏢: bulletConsume 2
	skills.Register(skills.Handler{ID: 4101009, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // 二段跳
	strike(4101010, either())                                                                     // 護身神風: bulletConsume 2
	passive(4110008, shadowResistance)                                                            // 激進黑暗: mhpR, asrR, terR
	skills.Register(skills.Handler{ID: 4110011, Kind: skills.KindPassive | skills.KindMobStatus}) // 飛毒殺: see venom
	passive(4110012, func(c *skills.PassiveContext) skills.Bonus {                                // 鏢術精通: damR; prop keeps the star (see stars)
		if weaponType(c.Character) != weaponClaw {
			return skills.Bonus{}
		}
		return skills.Bonus{DamagePercent: c.Values["damR"]}
	})
	passive(4110014, alchemist)                                                                   // 藥劑精通: x-100 potion recovery
	skills.Register(skills.Handler{ID: 4111009, Kind: skills.KindBuff, Cast: shadowStars})        // 無形鏢
	strike(4111010, either())                                                                     // 三飛閃
	strike(4111013, either())                                                                     // 陰影分裂: bulletConsume 3
	skills.Register(skills.Handler{ID: 4120011, Kind: skills.KindPassive | skills.KindMobStatus}) // 致命的飛毒殺: see venom
	passive(4120012, expertMastery(weaponClaw))                                                   // 暗器精通
	strike(4121013, either())                                                                     // 四飛閃
	skills.Register(skills.Handler{ID: 4121014, Kind: skills.KindBuff, Cast: attackBuff("indiePad")})
	skills.Register(skills.Handler{ID: 4121015, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // 絕對領域: cost only (area debuff)
	strike(4121016, either(noAmmo, prepare(suddenRaid)))                                          // 穢土轉生.改

	// 俠盜 420, 神偷 421, 暗影神偷 422
	passive(4200007, physicalTraining)
	passive(4200010, func(c *skills.PassiveContext) skills.Bonus { // 強化盾: x percent defence, er dodge
		return skills.Bonus{WDEFPercent: c.Values["x"], MDEFPercent: c.Values["x"], AvoidRate: c.Values["er"]}
	})
	skills.Register(skills.Handler{ID: 4201008, Kind: skills.KindSpecial, Cast: skills.CostOnly})               // 二段跳
	skills.Register(skills.Handler{ID: 4201009, Kind: skills.KindBuff, Cast: skills.StandardBuff})              // 輪迴: pad
	skills.Register(skills.Handler{ID: 4201011, Kind: skills.KindBuff, Cast: mesoGuard})                        // 楓幣護盾
	skills.Register(skills.Handler{ID: 4210010, Kind: skills.KindPassive | skills.KindMobStatus})               // 飛毒殺: see venom
	passive(4210012, greed)                                                                                     // 格雷德: mesoR
	passive(4210013, shadowResistance)                                                                          // 激進黑暗
	strike(4211011, melee(pickpocketOn))                                                                        // 高速鋒刃: 8 lines
	skills.Register(skills.Handler{ID: 4220011, Kind: skills.KindPassive | skills.KindMobStatus})               // 致命的飛毒殺: see venom
	passive(4220012, expertMastery(weaponDagger))                                                               // 進階精準之刀
	strike(4221010, either(noAmmo, prepare(suddenRaid)))                                                        // 穢土轉生.改
	skills.Register(skills.Handler{ID: 4221013, Kind: skills.KindBuff, Cast: attackBuff("x")})                  // 暗影神偷本能: x attack; the kill-point bonus is not applied
	for _, id := range []int{4001013, 4101008, 4101010, 4111010, 4111013, 4121013, 4121016, 4211011, 4221010} { // Venom poisons on these too
		venomAttacks[id] = true
	}
}

// physicalTraining (4100007, 4200007) adds lukX and dexX.
func physicalTraining(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{LUK: c.Values["lukX"], DEX: c.Values["dexX"]}
}

// expertMastery (4120012 claw, 4220012 dagger) is the fourth job mastery:
// mastery, x attack and the accR/evaR percents, with the weapon in hand.
func expertMastery(weapon int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		if weaponType(c.Character) != weapon {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.Values["mastery"], WATK: c.Values["x"], ACCPercent: c.Values["accR"], AVOIDPercent: c.Values["evaR"]}
	}
}

// attackBuff is a self buff of the WZ key as weapon attack for the WZ time
// (4121014 indiePad, 4221013 x). Their ignoreMobpdpR has no buff stat here.
func attackBuff(key string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		return selfBuff(c, stat("WATK", c.Value(key)))
	}
}

// suddenRaid (4121016, 4221010) leaves a poison: dot percent of the base
// damage every second for dotTime.
func suddenRaid(ctx *combat.AttackContext) error {
	if ctx.Effect.DOT <= 0 || ctx.Effect.DOTTime <= 0 {
		return nil
	}
	tick := max(1, int64(ctx.Effect.DOT)*combat.MaxBaseDamage(ctx.Stats)/100)
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{
		Stat: combat.MobPoison, Poison: true, Value: int(min(tick, 0x7fffffff)), Duration: ctx.Effect.DOTTime,
	})
	return nil
}
