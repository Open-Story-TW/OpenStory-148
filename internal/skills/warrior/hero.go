package warrior

import (
	"errors"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

// Fighter 110, Crusader 111, Hero 112.
func init() {
	// Fighter
	skills.Register(skills.Handler{ID: 1100000, Kind: skills.KindPassive, Passive: mastery(oneHandedSword, oneHandedAxe, oneHandedBlunt, twoHandedSword, twoHandedAxe, twoHandedBlunt)}) // Weapon Mastery
	skills.Register(skills.Handler{ID: 1100002, Kind: skills.KindAttack, Attack: melee(nil)})                                                                                            // Final Attack
	skills.Register(skills.Handler{ID: 1100009, Kind: skills.KindPassive, Passive: physicalTraining})                                                                                    // Physical Training
	skills.Register(skills.Handler{ID: 1101004, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})                                                                                     // Weapon Booster
	skills.Register(skills.Handler{ID: 1101006, Kind: skills.KindPartyBuff, Cast: skills.StandardPartyBuff})                                                                             // Rage: pad
	skills.Register(skills.Handler{ID: 1101007, Kind: skills.KindBuff, Cast: value("POWERGUARD", "x")})                                                                                  // Power Guard
	skills.Register(skills.Handler{ID: 1101008, Kind: skills.KindAttack, Attack: melee(nil)})                                                                                            // Ground Smash
	skills.Register(skills.Handler{ID: 1101010, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                                                                        // Rush (movement)

	// Crusader
	skills.Register(skills.Handler{ID: 1110000, Kind: skills.KindPassive, Passive: mpRecovery, OnTick: skills.ReviewedRecoveryTick, TickEvery: 4 * time.Second}) // Improving MP Recovery
	skills.Register(skills.Handler{ID: 1110009, Kind: skills.KindPassive, Passive: chanceAttack})                                                                // Chance Attack
	skills.Register(skills.Handler{ID: 1111002, Kind: skills.KindBuff, Cast: combo, OnAttack: comboOrbs})                                                        // Combo Attack
	skills.Register(skills.Handler{ID: 1111003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(darkness, finisher)})                              // Panic: DARKNESS x, 1 combo orb
	skills.Register(skills.Handler{ID: 1111005, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(stun, finisher)})                                  // Coma: STUN, 2 combo orbs
	skills.Register(skills.Handler{ID: 1111007, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: magicCrash()})                                        // Magic Crash
	skills.Register(skills.Handler{ID: 1111008, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(stun)})                                            // Shout: STUN
	skills.Register(skills.Handler{ID: 1111010, Kind: skills.KindAttack, Attack: melee(nil)})                                                                    // 雙連斬: ignoreMobpdpR

	// Hero
	skills.Register(skills.Handler{ID: 1120003, Kind: skills.KindPassive})                                                           // Advanced Combo: max orbs and double-orb prop, read by the combo counter
	skills.Register(skills.Handler{ID: 1120012, Kind: skills.KindPassive, Passive: combatMastery})                                   // Combat Mastery
	skills.Register(skills.Handler{ID: 1120013, Kind: skills.KindAttack | skills.KindPassive, Attack: melee(nil), Passive: attackX}) // Advanced Final Attack
	skills.Register(skills.Handler{ID: 1121000, Kind: skills.KindPartyBuff, Cast: mapleWarrior})
	skills.Register(skills.Handler{ID: 1121001, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(stun)}) // Monster Magnet: STUN
	skills.Register(skills.Handler{ID: 1121002, Kind: skills.KindBuff, Cast: value("STANCE", "prop")})                // Power Stance
	skills.Register(skills.Handler{ID: 1121006, Kind: skills.KindAttack, Attack: melee(nil)})                         // Rush
	skills.Register(skills.Handler{ID: 1121008, Kind: skills.KindAttack, Attack: melee(nil)})                         // Brandish
	skills.Register(skills.Handler{ID: 1121010, Kind: skills.KindBuff, Cast: enrage})                                 // Enrage
	skills.Register(skills.Handler{ID: 1121011, Kind: skills.KindSpecial, Cast: skills.HerosWill})
}

// combo (1111002) starts the orb counter at one orb (Java COMBO = 1, the
// value is orbs+1).
func combo(c *skills.CastContext) (skills.CastPlan, error) { return buff(c, stat("COMBO", 1)) }

// comboOrbs is Java handleOrbgain / handleOrbconsume on the owner's attacks:
// Panic uses 1 orb and Coma 2 (never below COMBO 1); Shout gives none; any
// other attack adds one orb up to x (Advanced Combo's x when learned, which
// also adds a second orb at prop%). The remaining duration is kept.
func comboOrbs(c *skills.TriggerContext) *skills.BuffChange {
	value, ok := c.Stat("COMBO")
	if !ok || len(c.Rows) == 0 || c.Attack == nil {
		return nil
	}
	next := value
	switch c.Attack.SkillID {
	case 1111002, 1111008:
		return nil
	case 1111003:
		next = max(1, value-1)
	case 1111005:
		next = max(1, value-2)
	default:
		limit, prop := c.Value("x"), 0
		if lv := learnedLevel(c.Character, 1120003); lv > 0 {
			if v, err := skillValues(1120003, lv); err == nil {
				limit, prop = v["x"], v["prop"]
			}
		}
		if value >= limit+1 {
			return nil
		}
		next = value + 1
		if prop > 0 && next < limit+1 && rand.IntN(100) < prop {
			next++
		}
	}
	if next == value {
		return nil
	}
	return &skills.BuffChange{Buff: &skills.Buff{Duration: c.Rows[0].ExpiresAt.Sub(c.Now), Stats: []skills.StatValue{stat("COMBO", next)}}}
}

// enrage (1121010) is Java ENRAGE = x*100+mobCount plus the Legacy SHARP_EYES
// 5200 bonus; it needs 10 combo orbs (COMBO 11) and uses them (Java
// handleOrbconsume(10), never below COMBO 1).
func enrage(c *skills.CastContext) (skills.CastPlan, error) {
	if v, ok := skills.ActiveStat(c.Caster, "COMBO", c.Now); !ok || v < 11 {
		return skills.CastPlan{}, errors.New("enrage needs 10 combo orbs")
	}
	mobCount := c.Value("mobCount")
	if mobCount == 0 {
		mobCount = 1
	}
	p, err := buff(c, stat("ENRAGE", c.Value("x")*100+mobCount), stat("SHARP_EYES", 5200))
	p.Consume = []skills.StatValue{stat("COMBO", 10)}
	return p, err
}

// darkness is Java MonsterStatus.DARKNESS = x (Panic).
var darkness = []combat.MobStatusApply{{Stat: combat.MobDarkness, ValueFrom: "x"}}

// mpRecovery (1110000) is Java relocHeal: hp and mp more per regeneration
// tick (Java also shortens the tick to 4s, which the regen loop owns).
func mpRecovery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{HPRecovery: c.Values["hp"], MPRecovery: c.Values["mp"]}
}

// chanceAttack (1110009) is Java dam_r *= damage/100; Legacy applies it to
// every target, not only stunned/dark/frozen ones.
func chanceAttack(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{DamagePercent: c.Values["damage"] - 100}
}

// combatMastery (1120012) is Java ignoreTargetDEF += ignoreMobpdpR.
func combatMastery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{IgnoreDefense: c.Values["ignoreMobpdpR"]}
}

// attackX is Java watk += padX (Advanced Final Attack).
func attackX(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{WATK: c.Values["padX"]} }
