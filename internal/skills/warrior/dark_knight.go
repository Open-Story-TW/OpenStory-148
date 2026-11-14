package warrior

import (
	"errors"
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

// Spearman 130, Dragon Knight 131, Dark Knight 132.
func init() {
	// Spearman
	skills.Register(skills.Handler{ID: 1300000, Kind: skills.KindPassive, Passive: mastery(spear, polearm)}) // Weapon Mastery
	skills.Register(skills.Handler{ID: 1300002, Kind: skills.KindAttack, Attack: spearAttack(nil)})          // Final Attack
	skills.Register(skills.Handler{ID: 1300009, Kind: skills.KindPassive, Passive: physicalTraining})        // Physical Training
	skills.Register(skills.Handler{ID: 1301004, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})         // Weapon Booster
	skills.Register(skills.Handler{ID: 1301006, Kind: skills.KindPartyBuff, Cast: skills.StandardPartyBuff}) // Iron Will: pdd, mdd
	skills.Register(skills.Handler{ID: 1301007, Kind: skills.KindPartyBuff, Cast: hyperBody})
	skills.Register(skills.Handler{ID: 1301008, Kind: skills.KindAttack, Attack: spearAttack(nil)}) // Ground Smash
	skills.Register(skills.Handler{ID: 1301010, Kind: skills.KindSpecial, Cast: skills.CostOnly})   // Rush (movement)

	// Dragon Knight
	skills.Register(skills.Handler{ID: 1310000, Kind: skills.KindPassive, Passive: elementalResistance})                    // Elemental Resistance
	skills.Register(skills.Handler{ID: 1310009, Kind: skills.KindPassive, Passive: dragonJudgement})                        // Dragon Judgement
	skills.Register(skills.Handler{ID: 1311001, Kind: skills.KindAttack, Attack: spearAttack(nil)})                         // Spear Crusher
	skills.Register(skills.Handler{ID: 1311003, Kind: skills.KindAttack, Attack: spearAttack(nil)})                         // Dragon Fury
	skills.Register(skills.Handler{ID: 1311005, Kind: skills.KindAttack, Attack: spearAttack(nil, hpRatio, ignoreDefense)}) // Sacrifice: x% HP, ignores defence
	skills.Register(skills.Handler{ID: 1311006, Kind: skills.KindAttack, Attack: spearAttack(nil, hpRatio)})                // Dragon Roar: x% HP (Java has no monster status; WZ mes=stun)
	skills.Register(skills.Handler{ID: 1311007, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: magicCrash()})   // Magic Crash
	skills.Register(skills.Handler{ID: 1311008, Kind: skills.KindBuff, Cast: dragonBlood})

	// Dark Knight
	skills.Register(skills.Handler{ID: 1320006, Kind: skills.KindPassive, Passive: berserk})                                // Berserk
	skills.Register(skills.Handler{ID: 1320008, Kind: skills.KindPassive | skills.KindSummon})                              // Aura of the Beholder: heal from the summon
	skills.Register(skills.Handler{ID: 1320009, Kind: skills.KindPassive | skills.KindSummon})                              // Hex of the Beholder: buffs from the summon
	skills.Register(skills.Handler{ID: 1320011, Kind: skills.KindPassive | skills.KindSummon, OnDamaged: beholderRevenge})  // Revenge of the Evil Eye
	skills.Register(skills.Handler{ID: 1321000, Kind: skills.KindPartyBuff, Cast: mapleWarrior})                            // Maple Warrior
	skills.Register(skills.Handler{ID: 1321001, Kind: skills.KindAttack | skills.KindMobStatus, Attack: spearAttack(stun)}) // Monster Magnet: STUN
	skills.Register(skills.Handler{ID: 1321002, Kind: skills.KindBuff, Cast: value("STANCE", "prop")})                      // Power Stance
	skills.Register(skills.Handler{ID: 1321003, Kind: skills.KindAttack, Attack: spearAttack(nil)})                         // Rush
	skills.Register(skills.Handler{ID: 1321007, Kind: skills.KindSummon | skills.KindBuff, Cast: beholderBuff, Attack: beholder})
	skills.Register(skills.Handler{ID: 1321010, Kind: skills.KindSpecial, Cast: skills.HerosWill})  // Hero's Will
	skills.Register(skills.Handler{ID: 1321012, Kind: skills.KindAttack, Attack: spearAttack(nil)}) // 連環攻擊
}

// hyperBody (1301007) is Java MAXHP and MAXMP percent (WZ x and y).
func hyperBody(c *skills.CastContext) (skills.CastPlan, error) {
	return partyBuff(c, stat("MAXHP", c.Value("x")), stat("MAXMP", c.Value("y")))
}

// dragonBlood (1311008) is the generic STR statup plus Java's
// DRAGONBLOOD = x (0 in v145: no HP drain).
func dragonBlood(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("STR", c.Value("str")), stat("DRAGONBLOOD", c.Value("x")))
}

// dragonJudgement (1310009) is Java passive_sharpeye_rate += cr and
// passive_sharpeye_min_percent += criticaldamageMin; the prop/x HP drain
// on attack belongs to the attack pipeline.
func dragonJudgement(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{Critical: c.Values["cr"], CriticalDamageMin: c.Values["criticaldamageMin"]}
}

// elementalResistance (1310000) is Java TER += x.
func elementalResistance(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{TER: c.Values["x"]}
}

// berserk (1320006) is Java dam_r *= (damage+100)/100; Legacy applies it at
// any HP although the WZ text limits it to HP above x%.
func berserk(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{DamagePercent: c.Values["damage"]}
}

// mapleWarrior is the 4th-job party buff of every Adventurer branch (Java
// MAPLE_WARRIOR = x percent of the base stats).
func mapleWarrior(c *skills.CastContext) (skills.CastPlan, error) {
	return partyBuff(c, stat("MAPLE_WARRIOR", c.Value("x")))
}

// spearAttack is a Dragon Knight line attack: melee plus the Dragon
// Judgement lifesteal.
func spearAttack(onHit []combat.MobStatusApply, prepare ...func(*combat.AttackContext) error) *combat.AttackSpec {
	s := melee(onHit, prepare...)
	s.Drain = dragonJudgementDrain
	return s
}

// dragonJudgementDrain is Java MapleCharacter.onAttack with 1310009
// learned: at prop% heal x% of the damage, at most half the max HP.
func dragonJudgementDrain(ctx *combat.AttackContext, dealt int64) (int, int) {
	prop, x := dragonJudgementValues(ctx.Character.Skills)
	if x <= 0 || rand.IntN(100) > prop {
		return 0, 0
	}
	return drained(dealt, x, ctx.Character.MaxHP), 0
}

func dragonJudgementValues(learned []domain.SkillState) (prop, x int) {
	lv := learnedLevel(domain.Character{Skills: learned}, 1310009)
	if lv < 1 {
		return 0, 0
	}
	v, err := skillValues(1310009, lv)
	if err != nil {
		return 0, 0
	}
	return v["prop"], v["x"]
}

func drained(dealt int64, percent, maxHP int) int {
	return int(min(dealt*int64(percent)/100, int64(maxHP/2)))
}

// beholder is the Beholder: follows its owner and heals or buffs it on the
// client's RecvSubSummon (Java SummonMovementType.FOLLOW, getSummonType 2).
var beholder = &combat.AttackSpec{Summon: &combat.SummonSpec{MoveAbility: 1, AssistType: 2, Sub: beholderSub}}

// beholderSub is Java SummonHandler 1321007: Aura (1320008) heals hp at most
// every x seconds with animation 5; Hex (1320009) applies its buff with
// animation 6-8, which needs an S0a buff entry point outside the cast path.
func beholderSub(c combat.SubSummonContext) (combat.SubSummonResult, error) {
	interval := time.Duration(c.SubValues["x"]) * time.Second
	if !c.LastSub.IsZero() && c.Now.Sub(c.LastSub) < interval {
		return combat.SubSummonResult{}, errors.New("beholder sub-skill on cooldown")
	}
	switch c.SubSkill {
	case 1320008:
		return combat.SubSummonResult{HealHP: c.SubValues["hp"], Action: 5}, nil
	case 1320009:
		b := hexBuff(c.SubValues)
		owner, level := c.Character.ID, c.SubLevel
		return combat.SubSummonResult{Action: byte(6 + rand.IntN(3)), Apply: func() {
			skills.ApplyBuff(owner, 1320009, level, skills.BuffChange{Buff: b})
		}}, nil
	}
	return combat.SubSummonResult{}, fmt.Errorf("skill%d is not a beholder sub-skill", c.SubSkill)
}

// hexBuff is Java's generic statups of 1320009 (healEffect.applyTo): epad,
// epdd, emdd, acc and eva for time seconds.
func hexBuff(v map[string]int) *skills.Buff {
	b := &skills.Buff{Duration: time.Duration(v["time"]) * time.Second}
	for _, s := range []struct {
		key  string
		stat buffstat.Stat
	}{{"epad", "ENHANCED_WATK"}, {"epdd", "ENHANCED_WDEF"}, {"emdd", "ENHANCED_MDEF"}, {"acc", "ACC"}, {"eva", "AVOID"}} {
		if v[s.key] != 0 {
			b.Stats = append(b.Stats, stat(s.stat, v[s.key]))
		}
	}
	return b
}

// beholderRevenge (1320011) is Java MapleCharacter damage handling for a
// Dark Knight hit by a monster: with the Beholder out and the skill off
// cooldown, at prop% the Beholder hits the attacker for damage% of the max
// base damage and the cooltime starts.
func beholderRevenge(c *skills.TriggerContext) *skills.BuffChange {
	if c.Damage <= 0 || c.Mob == 0 || c.Character.Job != 132 || !skills.ActiveBuff(c.Character, 1321007, c.Now) {
		return nil
	}
	for _, cool := range c.Character.SkillCooldowns {
		if cool.ID == c.Skill.ID && cool.ExpiresAt.After(c.Now) {
			return nil
		}
	}
	if rand.IntN(100) >= c.Value("prop") {
		return nil
	}
	stats, err := combat.AttackStats(c.Character, c.Now)
	if err != nil {
		return nil
	}
	damage := combat.MaxBaseDamage(stats) * int64(c.Value("damage")) / 100
	return &skills.BuffChange{
		Cooldown: time.Duration(c.Value("cooltime")) * time.Second,
		MobHits:  []combat.ServerHit{{ObjectID: c.Mob, Damage: int(min(damage, int64(combat.MaxLineDamage())))}},
	}
}

// beholderBuff (1321007) is Java BEHOLDER = skill level; the core spawns the
// summon from the registered SummonSpec.
func beholderBuff(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("BEHOLDER", c.Level))
}
