package thief

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"time"
)

// Dual Blade (430 Blade Recruit … 434 Blade Master).
func init() {
	skills.Register(skills.Handler{ID: 4300000, Kind: skills.KindPassive, Passive: kataraMastery})
	skills.Register(skills.Handler{ID: 4301001, Kind: skills.KindAttack, Attack: melee()}) // Triple Stab
	skills.Register(skills.Handler{ID: 4301002, Kind: skills.KindBuff, Cast: booster})

	skills.Register(skills.Handler{ID: 4310004, Kind: skills.KindPassive, Passive: shadowResistance})
	skills.Register(skills.Handler{ID: 4311001, Kind: skills.KindBuff, Cast: skills.StandardBuff}) // Self Haste: never a party buff (Java isPartyBuff)
	skills.Register(skills.Handler{ID: 4311002, Kind: skills.KindAttack, Attack: melee()})         // Fatal Blow
	skills.Register(skills.Handler{ID: 4311003, Kind: skills.KindAttack, Attack: melee()})         // Slash Storm

	skills.Register(skills.Handler{ID: 4321000, Kind: skills.KindBuff, Cast: tornadoSpin})
	skills.Register(skills.Handler{ID: 4321001, Kind: skills.KindAttack, Attack: melee()})                                                                                                    // Tornado Spin attack
	skills.Register(skills.Handler{ID: 4321002, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(combat.MobStatusApply{Stat: combat.MobDarkness, ValueFrom: "x"}), thrown)}) // Flashbang: a thrown melee attack (0x4B), not a monster cast
	skills.Register(skills.Handler{ID: 4321003, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                                                                             // Flash Jump
	skills.Register(skills.Handler{ID: 4321004, Kind: skills.KindAttack, Attack: melee()})                                                                                                    // Upper Stab

	skills.Register(skills.Handler{ID: 4330001, Kind: skills.KindPassive}) // Advanced Dark Sight: prop keeps Dark Sight on attack (darkSightBreak)
	skills.Register(skills.Handler{ID: 4330007, Kind: skills.KindPassive, OnAttack: lifeDrain})
	skills.Register(skills.Handler{ID: 4331000, Kind: skills.KindAttack, Attack: melee()}) // Bloody Storm
	skills.Register(skills.Handler{ID: 4331002, Kind: skills.KindBuff, Cast: mirrorImage})
	skills.Register(skills.Handler{ID: 4331003, Kind: skills.KindAttack | skills.KindBuff, Attack: melee(), OnAttack: owlSpirit}) // Owl Spirit
	skills.Register(skills.Handler{ID: 4331005, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(stun))})        // Flying Assaulter
	skills.Register(skills.Handler{ID: 4331006, Kind: skills.KindAttack, Attack: melee()})                                        // Chains of Hell: cr 100

	skills.Register(skills.Handler{ID: 4340001, Kind: skills.KindPassive | skills.KindMobStatus})  // Venom
	skills.Register(skills.Handler{ID: 4340010, Kind: skills.KindPassive, Passive: criticalThrow}) // Sharpness: prop, criticaldamageMin
	skills.Register(skills.Handler{ID: 4341000, Kind: skills.KindPartyBuff, Cast: mapleWarrior})
	skills.Register(skills.Handler{ID: 4341002, Kind: skills.KindAttack | skills.KindBuff, Attack: melee(func(s *combat.AttackSpec) { s.Prepare = chain(s.Prepare, finalCutCost) }), OnAttack: finalCut})          // Final Cut
	skills.Register(skills.Handler{ID: 4341003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(combat.MobStatusApply{Stat: combat.MobTimeBomb, Value: 1, Explode: true, TickFrom: "damage"}))}) // Monster Bomb: charge
	skills.Register(skills.Handler{ID: 4341004, Kind: skills.KindAttack, Attack: melee()})                                                                                                                         // Blade Fury
	skills.Register(skills.Handler{ID: 4341006, Kind: skills.KindSummon | skills.KindPassive, Cast: mirroredTarget, Passive: mirroredTargetPassive, Attack: mirroredTargetSummon})                                 // HP from WZ x
	skills.Register(skills.Handler{ID: 4341007, Kind: skills.KindBuff, Cast: thorns})
	skills.Register(skills.Handler{ID: 4341008, Kind: skills.KindSpecial, Cast: skills.HerosWill}) // Hero's Will
	skills.Register(skills.Handler{ID: 4341009, Kind: skills.KindAttack, Attack: melee()})         // Phantom Blow
}

// kataraMastery (4300000) adds mastery and x accuracy with a dagger or katara
// for Dual Blade jobs.
func kataraMastery(c *skills.PassiveContext) skills.Bonus {
	if w := weaponType(c.Character); w != weaponDagger && w != weaponKatara || !dualBlade(c.Character.Job) {
		return skills.Bonus{}
	}
	return skills.Bonus{Mastery: c.Values["mastery"], ACC: c.Values["x"]}
}

// tornadoSpin (4321000) is the dash pair DASH_SPEED 100+x, DASH_JUMP y for one
// second (Java overrides the duration).
func tornadoSpin(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Self = &skills.Buff{Duration: time.Second, Stats: []skills.StatValue{stat("DASH_SPEED", 100+c.Value("x")), stat("DASH_JUMP", c.Value("y"))}}
	return p, nil
}

// mirrorImage (4331002) is SHADOWPARTNER x; Java isMI allows only 433/434.
func mirrorImage(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Caster.Job != 433 && c.Caster.Job != 434 {
		return skills.StandardCost(c), fmt.Errorf("skill%d: job %d", c.Skill.ID, c.Caster.Job)
	}
	return selfBuff(c, stat("SHADOWPARTNER", c.Value("x")))
}

// finalCut (4341002) is Java's effect.applyTo after the charged attack hits:
// FINAL_CUT y for 900 seconds (Legacy overrides the WZ time). Java keeps
// ATTACK_BUFF x in its own statups but sends only FINAL_CUT to the client.
func finalCut(c *skills.TriggerContext) *skills.BuffChange {
	if c.Attack == nil || c.Attack.SkillID != 4341002 || len(c.Attack.Targets) == 0 {
		return nil
	}
	return &skills.BuffChange{Buff: &skills.Buff{Duration: 900 * time.Second, Stats: []skills.StatValue{stat("FINAL_CUT", c.Value("y"))}}}
}

// finalCutCost is the Java hpR = -x/100 of Final Cut, paid with the attack.
func finalCutCost(ctx *combat.AttackContext) error {
	ctx.Effect.HPRatio = ctx.Effect.X
	return nil
}

// thorns (4341007) is ENHANCED_WATK epad; Legacy leaves the STANCE prop out.
func thorns(c *skills.CastContext) (skills.CastPlan, error) {
	return selfBuff(c, stat("ENHANCED_WATK", c.Value("epad")))
}

// mirroredTargetPassive (4341006) adds pddR/mddR percent defense and er dodge
// for Blade Lord/Master (Java PlayerStats 433–434).
func mirroredTargetPassive(c *skills.PassiveContext) skills.Bonus {
	if c.Character.Job != 433 && c.Character.Job != 434 {
		return skills.Bonus{}
	}
	return skills.Bonus{WDEFPercent: c.Values["pddR"], MDEFPercent: c.Values["mddR"], AvoidRate: c.Values["er"]}
}

// owlSpirit (4331003) is Java's effect.applyTo after an Owl Spirit attack
// kills its target: OWL_SPIRIT y until x more attacks (battleshipHP), kept as
// the server-only counter. Every later attack that hits spends one
// (MapleCharacter.afterAttack); the attack that grants it does not.
func owlSpirit(c *skills.TriggerContext) *skills.BuffChange {
	a := c.Attack
	if a == nil || len(a.Targets) == 0 {
		return nil
	}
	if a.SkillID == 4331003 && len(a.Defeated) > 0 {
		return &skills.BuffChange{Buff: &skills.Buff{Duration: skills.Permanent, Counter: c.Value("x"), Stats: []skills.StatValue{stat("OWL_SPIRIT", c.Value("y"))}}}
	}
	if len(c.Rows) == 0 {
		return nil
	}
	n := c.Counter() - 1
	if n <= 0 {
		return &skills.BuffChange{Cancel: true}
	}
	return &skills.BuffChange{Buff: &skills.Buff{Duration: c.Rows[0].ExpiresAt.Sub(c.Now), Counter: n, Stats: []skills.StatValue{stat("OWL_SPIRIT", c.Rows[0].Value)}}}
}

// lifeDrain (4330007): Legacy hard-codes hpRecoverProp 10 (Randomizer
// nextInt(100) <= 10, so 11 percent) and 30 percent of the attack's damage,
// at most half of MaxHP, for jobs 433–434 (PlayerStats, MapleCharacter.onAttack).
func lifeDrain(c *skills.TriggerContext) *skills.BuffChange {
	if c.Attack == nil || c.Attack.Damage <= 0 || c.Character.Job != 433 && c.Character.Job != 434 || !roll(11) {
		return nil
	}
	return &skills.BuffChange{HealHP: int(min(c.Attack.Damage*30/100, int64(c.Character.MaxHP/2)))}
}
