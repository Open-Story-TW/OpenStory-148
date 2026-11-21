package warrior

import (
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

// Page 120, White Knight 121, Paladin 122.
func init() {
	// Page
	skills.Register(skills.Handler{ID: 1200000, Kind: skills.KindPassive, Passive: pageMastery})                      // Weapon Mastery
	skills.Register(skills.Handler{ID: 1200002, Kind: skills.KindAttack, Attack: melee(nil, charged)})                // Final Attack
	skills.Register(skills.Handler{ID: 1200009, Kind: skills.KindPassive, Passive: physicalTraining})                 // Physical Training
	skills.Register(skills.Handler{ID: 1201004, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})                  // Weapon Booster
	skills.Register(skills.Handler{ID: 1201006, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: threaten}) // Threaten
	skills.Register(skills.Handler{ID: 1201007, Kind: skills.KindBuff, Cast: value("POWERGUARD", "x"), OnDamaged: skills.PowerGuardReflect}) // Power Guard
	skills.Register(skills.Handler{ID: 1201008, Kind: skills.KindAttack, Attack: melee(nil, charged)})                // Ground Smash
	skills.Register(skills.Handler{ID: 1201010, Kind: skills.KindSpecial, Cast: skills.CostOnly})                     // Rush (movement)

	// White Knight
	skills.Register(skills.Handler{ID: 1210001, Kind: skills.KindPassive, Passive: shieldMastery}) // Shield Mastery
	chargedBlow := melee(stun, charged)
	chargedBlow.LearnedEffect = chargedBlowUpgrade
	skills.Register(skills.Handler{ID: 1211002, Kind: skills.KindAttack | skills.KindMobStatus, Attack: chargedBlow})
	skills.Register(skills.Handler{ID: 1211004, Kind: skills.KindBuff, Cast: charge})                                     // Flame Charge
	skills.Register(skills.Handler{ID: 1211006, Kind: skills.KindBuff, Cast: charge})                                     // Blizzard Charge
	skills.Register(skills.Handler{ID: 1211008, Kind: skills.KindBuff, Cast: charge})                                     // Lightning Charge
	skills.Register(skills.Handler{ID: 1211009, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: magicCrash()}) // Magic Crash
	skills.Register(skills.Handler{ID: 1211010, Kind: skills.KindHeal, Cast: restoration})                                // Restoration
	skills.Register(skills.Handler{ID: 1211011, Kind: skills.KindPartyBuff, Cast: func(c *skills.CastContext) (skills.CastPlan, error) {
		return partyBuff(c, stat("COMBAT_ORDERS", c.Value("x")))
	}}) // Combat Orders

	// Paladin
	skills.Register(skills.Handler{ID: 1220005, Kind: skills.KindPassive, Passive: achilles})                            // Achilles
	skills.Register(skills.Handler{ID: 1220006, Kind: skills.KindPassive, Passive: guardian})                            // Guardian: shield block (prop, stun) needs the S0a damage path
	skills.Register(skills.Handler{ID: 1220010, Kind: skills.KindPassive, Passive: elementalForce})                      // Elemental Force
	skills.Register(skills.Handler{ID: 1220013, Kind: skills.KindPassive | skills.KindBuff, OnDamaged: divineShieldHit}) // Divine Shield
	skills.Register(skills.Handler{ID: 1221000, Kind: skills.KindPartyBuff, Cast: mapleWarrior})                         // Maple Warrior
	skills.Register(skills.Handler{ID: 1221002, Kind: skills.KindBuff, Cast: value("STANCE", "prop")})                   // Power Stance
	skills.Register(skills.Handler{ID: 1221004, Kind: skills.KindBuff, Cast: charge})                                    // Divine Charge
	skills.Register(skills.Handler{ID: 1221007, Kind: skills.KindAttack, Attack: melee(nil, charged)})                   // Rush
	skills.Register(skills.Handler{ID: 1221009, Kind: skills.KindAttack, Attack: melee(nil, charged)})                   // Blast
	skills.Register(skills.Handler{ID: 1221011, Kind: skills.KindAttack, Attack: melee(nil, holy)})                      // Heaven's Hammer: holy
	skills.Register(skills.Handler{ID: 1221012, Kind: skills.KindSpecial, Cast: skills.HerosWill})                       // Hero's Will
}

// charge is a White Knight element charge (Java WK_CHARGE = x, self only:
// isPartyBuff excludes the charges despite their lt/rb). Lightning Charge
// cast over another charge adds LIGHTNING_CHARGE = 1 instead (Java
// applyBuffEffect 1211008).
func charge(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Skill.ID == 1211008 {
		for _, b := range c.Caster.SkillBuffs {
			if b.Stat == "WK_CHARGE" && b.ID != 1211008 && b.ExpiresAt.After(c.Now) {
				return buff(c, stat("LIGHTNING_CHARGE", 1))
			}
		}
	}
	return buff(c, stat("WK_CHARGE", c.Value("x")))
}

// The v148 melee path at 0x91D156 looks up a Paladin's learned Elemental
// Force. Its WZ affectedSkill=1211002 replaces Charged Blow's damage and counts.
func chargedBlowUpgrade(character domain.Character, now time.Time, effect *combat.Effect) error {
	if character.Job != 122 {
		return nil
	}
	level := skills.EffectiveLevel(character, 1220010, now)
	if level == 0 {
		return nil
	}
	def, err := content.ConfiguredSkillDefinition(1220010)
	if err != nil {
		return err
	}
	values, err := def.Values(level)
	if err != nil {
		return err
	}
	effect.Damage = values["damage"]
	effect.MobCount = values["mobCount"]
	effect.AttackCount = values["attackCount"]
	return nil
}

// shieldMastery (1210001) is Java percent_wdef/percent_mdef += x (Legacy
// does not check for a shield).
func shieldMastery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{WDEFPercent: c.Values["x"], MDEFPercent: c.Values["x"]}
}

// achilles (1220005) adds WDEF percent t and (1000-x)/10 received-damage
// reduction (v148 WZ x=950-5*lv -> 800 = 20% reduction at lv30).
func achilles(c *skills.PassiveContext) skills.Bonus {
	b := skills.Bonus{WDEFPercent: c.Values["t"]}
	if x := c.Values["x"]; x > 0 && x < 1000 {
		b.DamageReduce = (1000 - x) / 10
	}
	return b
}

// guardian (1220006) is Java ASR += asrR, TER += terR.
func guardian(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{ASR: c.Values["asrR"], TER: c.Values["terR"]}
}

// restoration (1211010) is Java hpR = x/100: heal x% of max HP.
func restoration(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Heal = &skills.Heal{HPPercent: c.Value("x")}
	return p, nil
}

// pageWeapons are the weapons Page Weapon Mastery (1200000) counts.
var pageWeapons = mastery(oneHandedSword, oneHandedAxe, oneHandedBlunt, twoHandedSword, twoHandedAxe, twoHandedBlunt)

// pageMastery is Weapon Mastery (1200000). While an element charge is up a
// learned Elemental Force supplies the mastery instead (original v148
// sub_6D8DDE); the accuracy stays.
func pageMastery(c *skills.PassiveContext) skills.Bonus {
	b := pageWeapons(c)
	if b.Mastery > 0 && elementCharged(c) {
		b.Mastery = c.BasicMastery(1220010)
	}
	return b
}

// elementalForce (1220010): the original v148 mastery lookup (sub_6D8DDE)
// returns its mastery in place of Weapon Mastery's, only while Weapon
// Mastery applies to the wielded weapon and a WK_CHARGE is active.
func elementalForce(c *skills.PassiveContext) skills.Bonus {
	if !elementCharged(c) || skills.EffectiveLevel(c.Character, 1200000, c.Now) < 1 {
		return skills.Bonus{}
	}
	if pageWeapons(c) == (skills.Bonus{}) {
		return skills.Bonus{}
	}
	return skills.Bonus{Mastery: c.Values["mastery"]}
}

func elementCharged(c *skills.PassiveContext) bool {
	_, ok := skills.ActiveStat(c.Character, "WK_CHARGE", c.Now)
	return ok
}

// divineShieldHit is Java MapleCharacter damage handling for 1220013: a
// live shield loses one block per monster hit and ends at zero; otherwise a
// hit procs the shield at prop% unless cooling down. The core blocks the
// damage while DIVINE_SHIELD is live; breaking starts the cooltime (WZ 30s,
// Legacy turns 30 into 10).
func divineShieldHit(c *skills.TriggerContext) *skills.BuffChange {
	if c.Damage <= 0 {
		return nil
	}
	if left, ok := c.Stat("DIVINE_SHIELD"); ok {
		if left <= 1 {
			return &skills.BuffChange{Cancel: true, Cooldown: kiCooldown(c.Value("cooltime"))}
		}
		rows := make([]skills.StatValue, 0, len(c.Rows))
		for _, r := range c.Rows {
			v := r.Value
			if r.Stat == "DIVINE_SHIELD" {
				v = left - 1
			}
			rows = append(rows, stat(buffstat.Stat(r.Stat), v))
		}
		return &skills.BuffChange{Buff: &skills.Buff{Duration: c.Rows[0].ExpiresAt.Sub(c.Now), Stats: rows}}
	}
	for _, cool := range c.Character.SkillCooldowns {
		if cool.ID == c.Skill.ID && cool.ExpiresAt.After(c.Now) {
			return nil
		}
	}
	if rand.IntN(100) >= c.Value("prop") {
		return nil
	}
	p, err := divineShield(&skills.CastContext{Skill: c.Skill, Level: c.Level, Values: c.Values, Caster: c.Character, Now: c.Now})
	if err != nil {
		return nil
	}
	return &skills.BuffChange{Buff: p.Self}
}

// kiCooldown is Legacy MapleStatEffect's cooltime hack: 20s and 30s become 10s.
func kiCooldown(seconds int) time.Duration {
	if seconds == 20 || seconds == 30 {
		seconds = 10
	}
	return time.Duration(seconds) * time.Second
}

// divineShield (1220013) is the buff the shield proc grants when the
// Paladin is hit (prop%): DIVINE_SHIELD = x+1 blocks and epad ENHANCED_WATK.
func divineShield(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("DIVINE_SHIELD", c.Value("x")+1), stat("ENHANCED_WATK", c.Value("epad")))
}

// threaten (1201006) is Java applyMonsterBuff: inside lt/rb, at prop%,
// monster WATK and WDEF x and DARKNESS z, all for subTime (Java isSubTime;
// Legacy passes the raw seconds as milliseconds, the WZ unit is seconds).
var threaten = &combat.AttackSpec{MobCast: &combat.MobCastSpec{UseBox: true, Statuses: []combat.MobStatusApply{
	{Stat: combat.MobPAD, ValueFrom: "x", SubTime: true},
	{Stat: combat.MobPDR, ValueFrom: "x", SubTime: true},
	{Stat: combat.MobDarkness, ValueFrom: "z", SubTime: true},
}}}
