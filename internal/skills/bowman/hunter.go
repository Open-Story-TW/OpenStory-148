package bowman

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// 獵人 (310), 遊俠 (311), 箭神 (312). Summons are in summons.go; Mortal Blow
// in triggers.go.
func init() {
	// Hunter.
	skills.Register(skills.Handler{ID: 3100000, Kind: skills.KindPassive, Passive: weaponMastery(bow, 3120005)})
	skills.Register(skills.Handler{ID: 3100001, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Final Attack: Java mobCount 6
	skills.Register(skills.Handler{ID: 3100006, Kind: skills.KindPassive, Passive: physicalTraining})
	skills.Register(skills.Handler{ID: 3101002, Kind: skills.KindBuff, Cast: buff(0, key("BOOSTER", "x"))})
	skills.Register(skills.Handler{ID: 3101003, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // Double Jump
	skills.Register(skills.Handler{ID: 3101004, Kind: skills.KindBuff, Cast: buff(bow, key("SOULARROW", "x"))})
	skills.Register(skills.Handler{ID: 3101005, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{onHit: stun, prepare: arrowBomb})})

	// Ranger.
	skills.Register(skills.Handler{ID: 3110007, Kind: skills.KindPassive, Passive: evasionBoost})
	skills.Register(skills.Handler{ID: 3111000, Kind: skills.KindBuff, Cast: buff(0, key("ENHANCED_WATK", "epad"), key("CONCENTRATE", "x"))})
	skills.Register(skills.Handler{ID: 3111003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{prepare: inferno})})
	skills.Register(skills.Handler{ID: 3111004, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{noAmmo: true, onHit: stun})}) // Arrow Rain
	skills.Register(skills.Handler{ID: 3111006, Kind: skills.KindAttack, Attack: shoot(rule{})})                                                 // Strafe: bulletCount 4
	skills.Register(skills.Handler{ID: 3111008, Kind: skills.KindAttack | skills.KindHeal, Attack: shoot(rule{drain: drainArrow})})

	// Bowmaster.
	skills.Register(skills.Handler{ID: 3120005, Kind: skills.KindPassive, Passive: expert(bow)})
	skills.Register(skills.Handler{ID: 3120006, Kind: skills.KindPassive, Passive: spiritLink(3111005)}) // buff stats ride the Phoenix cast (summons.go)
	skills.Register(skills.Handler{ID: 3120008, Kind: skills.KindAttack, Attack: shoot(rule{})})         // Advanced Final Attack: Java mobCount 6
	// 終極烈焰箭 (String.wz: Inferno upgraded): once learned the v145 client
	// sends 3120010 instead of 3111003 (MCP b-round8). Java Legacy also runs a
	// copy of the Divine Shield counter for job 312 with this skill; that is
	// not the v145 skill and is not implemented.
	skills.Register(skills.Handler{ID: 3120010, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{prepare: inferno})})
	skills.Register(skills.Handler{ID: 3120011, Kind: skills.KindPassive, Passive: marksmanship})
	skills.Register(skills.Handler{ID: 3121000, Kind: skills.KindPartyBuff, Cast: partyBuff(key("MAPLE_WARRIOR", "x"))})
	skills.Register(skills.Handler{ID: 3121002, Kind: skills.KindPartyBuff, Cast: partyBuff(sharpEyes)})
	skills.Register(skills.Handler{ID: 3121004, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Hurricane: keydown quirk is built into S0b
	// 爆發: v145 String.wz says DEX plus an x% extra avoid chance. Java sends
	// HAMSTRING x; its on-hit SPEED lasts y seconds and the WZ has no y, so
	// the monster side is a no-op and is not implemented.
	skills.Register(skills.Handler{ID: 3121007, Kind: skills.KindBuff, Cast: buff(0, key("DEX", "dex"), key("HAMSTRING", "x"))})
	skills.Register(skills.Handler{ID: 3121009, Kind: skills.KindSpecial, Cast: skills.HerosWill}) // 楓葉淨化
}

// arrowBomb (3101005) hits for x% of the base damage (Java rangedAttack
// basedamage *= x/100; the WZ level has no damage) and stuns at prop.
func arrowBomb(ctx *combat.AttackContext) error {
	ctx.Effect.Damage = ctx.Effect.X
	return nil
}

// inferno (3111003) is fire; Java isPoison (dot and dotTime) adds POISON for
// max(time, dotTime).
func inferno(ctx *combat.AttackContext) error {
	ctx.Effect.Element = combat.Fire
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobPoison, Poison: true, Duration: max(ctx.Effect.Time, ctx.Effect.DOTTime)})
	return nil
}

// drainArrow (3111008) heals x percent of the damage dealt, at most half of
// MaxHP (Java MapleCharacter afterAttack).
func drainArrow(ctx *combat.AttackContext, dealt int64) (int, int) {
	return int(min(dealt*int64(ctx.Effect.X)/100, int64(ctx.Character.MaxHP/2))), 0
}

// weaponMastery (3100000, 3200000) adds mastery and x accuracy while the
// family weapon is equipped (Java CalcPassive_Mastery).
func weaponMastery(family, advancedID int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		if weapon(c.Character) != family {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.BasicMastery(advancedID), ACC: c.Values["x"]}
	}
}

// expert adds mastery when wielding the ranged weapon type selected at native 0x6D8DDE.
func expert(family int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		b := skills.Bonus{WATK: c.Values["x"], CriticalDamageMin: c.Values["criticaldamageMin"]}
		if weapon(c.Character) == family {
			b.Mastery = c.Values["mastery"]
		}
		return b
	}
}

// physicalTraining (3100006, 3200006) adds strX STR and dexX DEX.
func physicalTraining(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
}

// evasionBoost (3110007, 3210007) is Java dodgeChance += er.
func evasionBoost(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{AvoidRate: c.Values["er"]}
}

// marksmanship (3120011, 3220009) ignores ignoreMobpdpR percent of monster
// defense (Java ignoreTargetDEF; its percent_hp reads mhpR, absent in v145).
func marksmanship(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{IgnoreDefense: c.Values["ignoreMobpdpR"]}
}

// sharpEyes (3121002, 3221002) packs critical rate x in the high byte and
// critical damage y in the low byte. Java uses criticaldamageMax, which the
// v145 WZ does not have (it would always send 0); y is the WZ critical damage.
var sharpEyes = stat{"SHARP_EYES", func(v map[string]int) int { return v["x"]<<8 | v["y"]&0xff }}
