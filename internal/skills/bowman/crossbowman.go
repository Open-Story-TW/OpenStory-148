package bowman

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"math"
	"math/bits"
)

// 弩弓手 (320), 狙擊手 (321), 神射手 (322). Summons are in summons.go; Mortal
// Blow in triggers.go.
func init() {
	combat.ScaleCeiling(piercingScale)
	combat.ModifyCeiling(strafeFrozenCeiling)

	// Crossbowman.
	skills.Register(skills.Handler{ID: 3200000, Kind: skills.KindPassive, Passive: weaponMastery(crossbow, 3220004)})
	skills.Register(skills.Handler{ID: 3200001, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Final Attack: Java mobCount 6
	skills.Register(skills.Handler{ID: 3200006, Kind: skills.KindPassive, Passive: physicalTraining})
	skills.Register(skills.Handler{ID: 3201002, Kind: skills.KindBuff, Cast: buff(0, key("BOOSTER", "x"))})
	skills.Register(skills.Handler{ID: 3201003, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // Double Jump
	skills.Register(skills.Handler{ID: 3201004, Kind: skills.KindBuff, Cast: buff(crossbow, key("SOULARROW", "x"))})
	skills.Register(skills.Handler{ID: 3201005, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Iron Arrow: pierces 6 mobs

	// Sniper.
	skills.Register(skills.Handler{ID: 3210007, Kind: skills.KindPassive, Passive: evasionBoost})
	// Sniper Concentrate (3211000) shares 3111000's WZ epad + x (MP cost
	// reduction) and v148 client handling (0x6AE422), requiring CONCENTRATE.
	skills.Register(skills.Handler{ID: 3211000, Kind: skills.KindBuff, Cast: buff(0, key("ENHANCED_WATK", "epad"), key("CONCENTRATE", "x"))})
	skills.Register(skills.Handler{ID: 3211003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{prepare: blizzard})})
	skills.Register(skills.Handler{ID: 3211004, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(rule{noAmmo: true, onHit: seal})}) // Arrow Eruption
	skills.Register(skills.Handler{ID: 3211006, Kind: skills.KindAttack, Attack: shoot(rule{prepare: ultimateStrafe})})
	skills.Register(skills.Handler{ID: 3211008, Kind: skills.KindAttack, Attack: shoot(rule{})}) // 龍魂之箭: range 440, piercingScale

	// Marksman.
	skills.Register(skills.Handler{ID: 3220004, Kind: skills.KindPassive, Passive: expert(crossbow)})
	skills.Register(skills.Handler{ID: 3220005, Kind: skills.KindPassive, Passive: spiritLink(3211005)}) // buff stats ride the Frostprey cast (summons.go)
	skills.Register(skills.Handler{ID: 3220009, Kind: skills.KindPassive, Passive: marksmanship})
	skills.Register(skills.Handler{ID: 3220010, Kind: skills.KindPassive}) // Ultimate Strafe: applied by 3211006's ultimateStrafe
	skills.Register(skills.Handler{ID: 3221000, Kind: skills.KindPartyBuff, Cast: partyBuff(key("MAPLE_WARRIOR", "x"))})
	skills.Register(skills.Handler{ID: 3221001, Kind: skills.KindAttack, Attack: shoot(rule{prepare: piercing})})
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

// ultimateStrafe applies 3220010 to Strafe: native v148 replaces Strafe's
// damage percent with 3220010's damage and uses 3220010's bulletCount.
func ultimateStrafe(ctx *combat.AttackContext) error {
	lv := learned(ctx.Character, 3220010)
	if lv < 1 {
		return nil
	}
	def, err := content.ConfiguredSkillDefinition(3220010)
	if err != nil {
		return err
	}
	v, err := def.Values(min(lv, def.MaxLevel))
	if err != nil {
		return err
	}
	if dmg := v["damage"]; dmg > 0 {
		ctx.Effect.Damage = dmg
	}
	ctx.Effect.BulletCount = max(ctx.Effect.BulletCount, v["bulletCount"])
	return nil
}

// strafeFrozenCeiling lifts 3211006's bound on monsters frozen by Blizzard
// (3211003). Returning ceiling unchanged on other targets preserves the normal
// critical envelope and mastery floor.
func strafeFrozenCeiling(c combat.DamageContext, ceiling int) int {
	if c.Effect.SkillID != 3211006 {
		return ceiling
	}
	if st := c.Statuses[combat.MobFreeze]; st != nil && st.SkillID == 3211003 {
		return combat.MaxLineDamage()
	}
	return ceiling
}

// piercing (3221001) fires 4 lines (Java rangedAttack bulletCount = 4).
func piercing(ctx *combat.AttackContext) error {
	ctx.Effect.BulletCount = max(ctx.Effect.BulletCount, 4)
	return nil
}

// piercingMultipliers matches the native v148 piercing table (0x6DAAC0):
// target index i (0..7) multiplies every line by 1.2^i = (6/5)^i.
var piercingFactors = [8]struct{ num, den uint64 }{
	{1, 1},
	{6, 5},
	{36, 25},
	{216, 125},
	{1296, 625},
	{7776, 3125},
	{46656, 15625},
	{279936, 78125},
}

// piercingScale scales the normal bound of piercing arrows (3211008 Dragon's
// Breath and 3221001 Piercing) by the largest 1.2^i factor within mobCount,
// keeping the critical envelope intact.
func piercingScale(c combat.DamageContext, ceiling int) int {
	if c.Effect.SkillID != 3211008 && c.Effect.SkillID != 3221001 || ceiling <= 0 {
		return ceiling
	}
	idx := min(max(1, c.Effect.MobCount), len(piercingFactors)) - 1
	if idx == 0 {
		return ceiling
	}
	f := piercingFactors[idx]
	hi, lo := bits.Mul64(uint64(ceiling), f.num)
	if hi >= f.den {
		return combat.MaxLineDamage()
	}
	q, r := bits.Div64(hi, lo, f.den)
	if r > 0 && q < math.MaxInt64 {
		q++
	}
	return min(int(min(q, uint64(math.MaxInt64))), combat.MaxLineDamage())
}
