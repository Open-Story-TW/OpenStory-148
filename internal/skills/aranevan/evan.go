package aranevan

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"time"
)

// elements is the WZ elemAttr of the Evan attacks (Skill.wz 22xx.img; the
// skill seed does not carry it).
var elements = map[int]combat.Element{
	22101000: combat.Fire, 22111000: combat.Lightning, 22121000: combat.Ice, 22151001: combat.Fire, 22151002: combat.Dark,
	22161001: combat.Fire, 22161002: combat.Dark, 22171003: combat.Fire, 22181001: combat.Fire, 22181002: combat.Dark,
}

// Evan 2200, 2210-2218 (the beginner 2001 belongs to common). Every Evan
// attack is the magic attack packet the dragon's action sends.
func init() {
	passive := func(id int, f func(*skills.PassiveContext) skills.Bonus) {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
	}
	cast := func(id int, kind skills.Kind, f func(*skills.CastContext) (skills.CastPlan, error)) {
		skills.Register(skills.Handler{ID: id, Kind: kind, Cast: f})
	}

	passive(22000000, dragonSoul)                                                                                 // Dragon Soul
	passive(22000002, nil)                                                                                        // Elemental Weakness (Java pvpSkills only)
	magic(22001001)                                                                                               // Magic Missile
	magic(22101000)                                                                                               // Fire Circle
	cast(22101001, skills.KindSpecial, skills.CostOnly)                                                           // Teleport
	magic(22111000)                                                                                               // Lightning Bolt
	cast(22111001, skills.KindBuff, value("MAGIC_GUARD", "x"))                                                    // Magic Guard
	passive(22120001, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{INT: c.Values["intX"]} }) // High Wisdom
	passive(22120002, spellMastery)                                                                               // Spell Mastery
	magic(22121000, freeze)                                                                                       // Ice Breath: FREEZE
	magic(22131000, stunOnHit)                                                                                    // Magic Flare: STUN
	cast(22131001, skills.KindPartyBuff, party(value("MAGIC_SHIELD", "x")))                                       // Magic Shield: pdd, mdd, mhpR
	cast(22131002, skills.KindBuff, value("ELEMENT_RESET", "x"))                                                  // Elemental Reset
	passive(22140000, criticalMagic)                                                                              // Critical Magic
	magic(22141001, stunOnHit)                                                                                    // Dragon Thrust: STUN
	cast(22141002, skills.KindBuff, value("BOOSTER", "x"))                                                        // Magic Booster
	// Dragon Blink: the client moves itself; the server re-rolls the teleport key (0x164).
	skills.Register(skills.Handler{ID: 22141004, Kind: skills.KindSpecial, Cast: skills.CostOnly, Attack: &combat.AttackSpec{TeleportKey: true}})
	passive(22150000, magicAmplification)                                                                                                                                                                                                                                                                       // Magic Amplification
	magic(22150004)                                                                                                                                                                                                                                                                                             // Dragon Spark (final attack)
	magic(22151001, stunOnHit)                                                                                                                                                                                                                                                                                  // Dragon Breath: STUN
	skills.Register(skills.Handler{ID: 22151002, Kind: skills.KindAttack | skills.KindMobStatus | skills.KindBuff, Attack: magicSpec(22151002), OnAttack: killerWings})                                                                                                                                         // Killer Wings: DoT, homing
	cast(22151003, skills.KindPartyBuff, party(value("MAGIC_RESISTANCE", "x")))                                                                                                                                                                                                                                 // Magic Resistance: mdd
	passive(22160000, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{DamagePercent: c.Values["damage"]} })                                                                                                                                                                                   // Dragon Fury
	magic(22161001)                                                                                                                                                                                                                                                                                             // Earthquake
	magic(22161002, imprint)                                                                                                                                                                                                                                                                                    // Phantom Imprint
	skills.Register(skills.Handler{ID: 22161003, Kind: skills.KindFieldObject | skills.KindHeal, Cast: skills.CostOnly, Attack: &combat.AttackSpec{Field: &combat.FieldSpec{Kind: combat.FieldArea, AreaType: protocol.AreaRecovery, Box: box(22161003), RecoverMP: true, Interval: 2500 * time.Millisecond}}}) // Recovery Aura
	cast(22161004, skills.KindPartyBuff, party(value("ONYX_SHROUD", "x")))                                                                                                                                                                                                                                      // Onyx Shroud
	skills.Register(skills.Handler{ID: 22161005, Kind: skills.KindBuff | skills.KindAttack, Cast: teleportMastery, Attack: withBody(magicSpec(22161005))})                                                                                                                                                                // Teleport Mastery
	passive(22170001, magicMastery)                                                                                                                                                                                                                                                                             // Magic Mastery
	cast(22171000, skills.KindPartyBuff, party(value("MAPLE_WARRIOR", "x")))                                                                                                                                                                                                                                    // Maple Warrior
	magic(22171002)                                                                                                                                                                                                                                                                                             // Illusion: WZ ignoreMobpdpR
	magic(22171003)                                                                                                                                                                                                                                                                                             // Flame Wheel: DoT
	cast(22171004, skills.KindSpecial, skills.HerosWill)                                                                                                                                                                                                                                                        // Hero's Will
	cast(22181000, skills.KindBuff, blessingOfTheOnyx)                                                                                                                                                                                                                                                          // Blessing of the Onyx
	magic(22181001, stunOnHit)                                                                                                                                                                                                                                                                                  // Blaze: STUN
	magic(22181002)                                                                                                                                                                                                                                                                                             // Dark Fog
	skills.Register(skills.Handler{ID: 22181003, Kind: skills.KindPartyBuff, Cast: party(soulStone), OnLethal: soulStoneRevive})                                                                                                                                                                                // Soul Stone
	cast(22181004, skills.KindBuff, onyxWill)                                                                                                                                                                                                                                                                   // Onyx Will

	combat.ModifyCeiling(imprintCeiling)
}

// onHit is a magic attack's monster status beyond the WZ DoT.
type onHit func(*combat.AttackContext) error

// stunOnHit is Java MonsterStatus.STUN = 1 for the WZ time at prop.
func stunOnHit(ctx *combat.AttackContext) error {
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, stun...)
	return nil
}

// imprint (22161002) is Java MonsterStatus.IMPRINT = x for the WZ time (the
// native index 34); imprintCeiling adds the x% damage.
func imprint(ctx *combat.AttackContext) error {
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobWeakness, ValueFrom: "x"})
	return nil
}

// imprintCeiling is Java DamageParse: a monster under Phantom Imprint takes
// x% more from every attacker.
func imprintCeiling(dc combat.DamageContext, ceiling int) int {
	if st := dc.Statuses[combat.MobWeakness]; st != nil && st.SkillID == 22161002 && st.Value > 0 {
		return int(min(int64(ceiling)+int64(ceiling)*int64(st.Value)/100, int64(combat.MaxLineDamage())))
	}
	return ceiling
}

// magicSpec is an Evan magic attack: the WZ element (neutral while Elemental
// Reset runs, Java DamageParse clears the elements), Java isPoison DoT, then
// the listed statuses.
func magicSpec(id int, hits ...onHit) *combat.AttackSpec {
	element := elements[id]
	return &combat.AttackSpec{Kind: combat.Magic, Prepare: func(ctx *combat.AttackContext) error {
		ctx.Effect.Element = element
		if activeBuff(ctx.Character, ctx.Now, 22131002) {
			ctx.Effect.Element = combat.Physical
		}
		if err := poison(ctx); err != nil {
			return err
		}
		for _, h := range hits {
			if err := h(ctx); err != nil {
				return err
			}
		}
		return nil
	}}
}

// magic registers an Evan magic attack.
func magic(id int, hits ...onHit) {
	kind := skills.KindAttack
	if len(hits) > 0 || hasDot(id) {
		kind |= skills.KindMobStatus
	}
	skills.Register(skills.Handler{ID: id, Kind: kind, Attack: magicSpec(id, hits...)})
}

// hasDot reports a WZ dot on the skill.
func hasDot(id int) bool {
	v, err := values(id, 1)
	return err == nil && v["dot"] > 0
}

// killerWings (22151002) marks the first monster the skill hit: HOMING_BEACON
// holds its object ID until replaced (Java setLinkMid and giveHoming,
// duration 2100000000 ms).
func killerWings(c *skills.TriggerContext) *skills.BuffChange {
	if c.Attack == nil || c.Attack.SkillID != c.Skill.ID || len(c.Attack.Targets) == 0 {
		return nil
	}
	return &skills.BuffChange{Buff: &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{stat("HOMING_BEACON", int(c.Attack.Targets[0]))}}}
}

// teleportMastery (22161005) toggles TELEPORT_MASTERY x until recast (Java
// 2100000000 ms); Java charges y MP instead of mpCon. Teleporting with it on
// is a magic attack.
func teleportMastery(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Value("x") < 1 || c.Value("y") < 0 {
		return skills.CastPlan{}, fmt.Errorf("skill%d: invalid values", c.Skill.ID)
	}
	return skills.CastPlan{MPCost: c.Value("y"), Toggle: true, Self: &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{stat("TELEPORT_MASTERY", c.Value("x"))}}}, nil
}

// blessingOfTheOnyx (22181000) is ENHANCED_MATK plus the generic epdd/emdd.
// Java writes 10 + x, but the level has no x; the WZ emad is that 10 + level.
func blessingOfTheOnyx(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, append([]skills.StatValue{stat("ENHANCED_MATK", c.Value("emad"))}, enhancedStats(c)...)...)
}

// soulStone (22181003) is SOUL_STONE 1 (Java isSoulStone makes it a party
// buff).
func soulStone(c *skills.CastContext) (skills.CastPlan, error) { return buff(c, stat("SOUL_STONE", 1)) }

// soulStoneRevive is Java MapleCharacter.playerDead with SOUL_STONE: instead
// of dying the owner keeps x% of MaxHP (x of the buff's level), is moved to
// the map's portal 0 and the stone is used up.
func soulStoneRevive(c *skills.TriggerContext) *skills.BuffChange {
	for _, r := range c.Rows {
		if r.Stat != "SOUL_STONE" {
			continue
		}
		v, err := values(22181003, max(r.Level, 1))
		if err != nil || v["x"] < 1 {
			return nil
		}
		return &skills.BuffChange{Cancel: true, ReviveHPPercent: min(v["x"], 100), ReviveAtSpawn: true}
	}
	return nil
}

// onyxWill (22181004) is Java ONYX_WILL = damage (100: the level has none)
// and STANCE = prop.
func onyxWill(c *skills.CastContext) (skills.CastPlan, error) {
	damage := c.Value("damage")
	if damage == 0 {
		damage = 100
	}
	return buff(c, stat("ONYX_WILL", damage), stat("STANCE", c.Value("prop")))
}

// evanJob reports a dragon-holding Evan job (2200, 2210-2218).
func evanJob(job int) bool { return job == 2200 || job >= 2210 && job <= 2218 }

// dragonSoul (22000000) is Java magic += its level for 2200-2218 (WZ mad = x).
func dragonSoul(c *skills.PassiveContext) skills.Bonus {
	if !evanJob(c.Character.Job) {
		return skills.Bonus{}
	}
	return skills.Bonus{MATK: c.Values["mad"]}
}

// spellMastery (22120002) adds x magic attack and the v148 cr with a wand or
// staff. Its mastery is replaced by Magic Mastery (22170001) once learned:
// v148 0x6D9733 returns the 22170001 mastery when present and only sums the
// two magic attack values.
func spellMastery(c *skills.PassiveContext) skills.Bonus {
	if w := weaponType(c.Character); w != 137 && w != 138 {
		return skills.Bonus{}
	}
	return skills.Bonus{Mastery: c.BasicMastery(22170001), MATK: c.Values["x"], Critical: c.Values["cr"]}
}

// criticalMagic (22140000) is Java passive_sharpeye_rate += prop and
// criticaldamageMin for 2214-2218.
func criticalMagic(c *skills.PassiveContext) skills.Bonus {
	if j := c.Character.Job; j < 2214 || j > 2218 {
		return skills.Bonus{}
	}
	return skills.Bonus{Critical: c.Values["prop"], CriticalDamageMin: c.Values["criticaldamageMin"]}
}

// magicAmplification (22150000) is Java's element amplification: MP costs
// become x% and damage y%.
func magicAmplification(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MPConsume: c.Values["x"] - 100, ElementAmp: c.Values["y"] - 100}
}

// magicMastery (22170001) is Java magic += x, mastery and criticaldamageMin.
func magicMastery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MATK: c.Values["x"], Mastery: c.Values["mastery"], CriticalDamageMin: c.Values["criticaldamageMin"]}
}

// withBody also accepts the spec's teleport body attack (0x4E, Teleport
// Mastery); PlanAttack keeps the magic ceiling for it.
func withBody(s *combat.AttackSpec) *combat.AttackSpec {
	s.Kind |= combat.Body
	return s
}
