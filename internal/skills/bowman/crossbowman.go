package bowman

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
)

// 弩弓手 (320), 狙擊手 (321), 神射手 (322). Summons are in summons.go; Mortal
// Blow in triggers.go.
func init() {
	// Crossbowman.
	skills.Register(skills.Handler{ID: 3200000, Kind: skills.KindPassive, Passive: weaponMastery(crossbow)})
	skills.Register(skills.Handler{ID: 3200001, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Final Attack: Java mobCount 6
	skills.Register(skills.Handler{ID: 3200006, Kind: skills.KindPassive, Passive: physicalTraining})
	skills.Register(skills.Handler{ID: 3201002, Kind: skills.KindBuff, Cast: buff(0, key("BOOSTER", "x"))})
	skills.Register(skills.Handler{ID: 3201003, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // Double Jump
	skills.Register(skills.Handler{ID: 3201004, Kind: skills.KindBuff, Cast: buff(crossbow, key("SOULARROW", "x"))})
	skills.Register(skills.Handler{ID: 3201005, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Iron Arrow: pierces 6 mobs

	// Sniper.
	skills.Register(skills.Handler{ID: 3210007, Kind: skills.KindPassive, Passive: evasionBoost})
	// Java has no CONCENTRATE case for 3211000, so only the generic epad stat.
	skills.Register(skills.Handler{ID: 3211000, Kind: skills.KindBuff, Cast: buff(0, key("ENHANCED_WATK", "epad"))})
	skills.Register(skills.Handler{ID: 3211003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{prepare: blizzard})})
	skills.Register(skills.Handler{ID: 3211004, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{noAmmo: true, onHit: seal})}) // Arrow Eruption
	skills.Register(skills.Handler{ID: 3211006, Kind: skills.KindAttack, Attack: shoot(rule{prepare: ultimateStrafe, maxDamage: strafeCeiling})})
	skills.Register(skills.Handler{ID: 3211008, Kind: skills.KindAttack, Attack: shoot(rule{})}) // 龍魂之箭: range 440

	// Marksman.
	skills.Register(skills.Handler{ID: 3220004, Kind: skills.KindPassive, Passive: expert})
	skills.Register(skills.Handler{ID: 3220005, Kind: skills.KindPassive, Passive: spiritLink(3211005)}) // buff stats ride the Frostprey cast (summons.go)
	skills.Register(skills.Handler{ID: 3220009, Kind: skills.KindPassive, Passive: marksmanship})
	skills.Register(skills.Handler{ID: 3220010, Kind: skills.KindPassive}) // Ultimate Strafe: applied by 3211006's ultimateStrafe
	skills.Register(skills.Handler{ID: 3221000, Kind: skills.KindPartyBuff, Cast: partyBuff(key("MAPLE_WARRIOR", "x"))})
	skills.Register(skills.Handler{ID: 3221001, Kind: skills.KindAttack, Attack: shoot(rule{prepare: piercing, maxDamage: piercingCeiling})})
	skills.Register(skills.Handler{ID: 3221002, Kind: skills.KindPartyBuff, Cast: partyBuff(sharpEyes)})
	// 爆發: like 3121007, Java sends BLIND x; its on-hit ACC lasts y seconds
	// and the WZ has no y, so the monster side is not implemented.
	skills.Register(skills.Handler{ID: 3221006, Kind: skills.KindBuff, Cast: buff(0, key("DEX", "dex"), key("BLIND", "x"))})
	skills.Register(skills.Handler{ID: 3221008, Kind: skills.KindSpecial, Cast: skills.HerosWill}) // 楓葉淨化
	skills.Register(skills.Handler{ID: 3221007, Kind: skills.KindAttack, Attack: shoot(rule{})})
}

// seal is Arrow Eruption's WZ effect (String.wz: seal at prop for time).
// Java Legacy has no monster status for 3211004; the WZ values are used.
var seal = []combat.MobStatusApply{{Stat: combat.MobSeal, Value: 1}}

// blizzard (3211003) is ice and freezes for twice the WZ time (Java FREEZE).
func blizzard(ctx *combat.AttackContext) error {
	ctx.Effect.Element = combat.Ice
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobFreeze, Value: 1, Duration: 2 * ctx.Effect.Time})
	return nil
}

// ultimateStrafe applies 3220010 to Strafe: Java PlayerStats damageIncrease
// (3211006) = damage(3220010) - 150 and the 3220010 bulletCount.
func ultimateStrafe(ctx *combat.AttackContext) error {
	lv := learned(ctx.Character, 3220010)
	if lv < 1 {
		return nil
	}
	def, err := content.SkillDefinition("", 3220010)
	if err != nil {
		return err
	}
	v, err := def.Values(min(lv, def.MaxLevel))
	if err != nil {
		return err
	}
	ctx.Effect.Damage += v["damage"] - 150
	ctx.Effect.BulletCount = max(ctx.Effect.BulletCount, v["bulletCount"])
	return nil
}

// strafeCeiling lifts the bound on monsters frozen by the caster's Blizzard
// (Java DamageParse: FREEZE from 3211003 makes the damage undefined).
func strafeCeiling(c combat.DamageContext) int {
	if st := c.Statuses[combat.MobFreeze]; st != nil && st.SkillID == 3211003 {
		return combat.MaxLineDamage()
	}
	return combat.LineCeiling(c)
}

// piercing (3221001) fires 4 lines (Java rangedAttack bulletCount = 4).
func piercing(ctx *combat.AttackContext) error {
	ctx.Effect.BulletCount = max(ctx.Effect.BulletCount, 4)
	return nil
}

// piercingCeiling is the line bound times mobCount (Java DamageParse).
func piercingCeiling(c combat.DamageContext) int {
	return min(combat.LineCeiling(c)*max(1, c.Effect.MobCount), combat.MaxLineDamage())
}
