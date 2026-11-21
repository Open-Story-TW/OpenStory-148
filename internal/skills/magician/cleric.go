package magician

import (
	"maple-go-server/internal/skills"
	"time"
)

// Cleric 230, Priest 231, Bishop 232.
func init() {
	passive(2300000, nil)                                                                                         // MP Eater: attack-pipeline proc
	passive(2300006, spellMastery)                                                                                // Spell Mastery
	passive(2300007, highWisdom)                                                                                  // High Wisdom
	skills.Register(skills.Handler{ID: 2301001, Kind: skills.KindSpecial, Cast: skills.CostOnly})                 // Teleport
	skills.Register(skills.Handler{ID: 2301003, Kind: skills.KindBuff, Cast: buff("INVINCIBLE", "x")})            // Invincible
	skills.Register(skills.Handler{ID: 2301004, Kind: skills.KindPartyBuff, Cast: party(buff("BLESS", "level"))}) // Bless
	magic(2301005)                                                                                                // Holy Arrow

	passive(2310008, holyFocus)                                                                                     // Holy Focus
	passive(2310010, criticalPassive)                                                                               // Critical passive: cr, criticaldamageMin
	skills.Register(skills.Handler{ID: 2311003, Kind: skills.KindPartyBuff, Cast: party(buff("HOLY_SYMBOL", "x"))}) // Holy Symbol
	magic(2311004, stun)                                                                                            // Shining Ray
	skills.Register(skills.Handler{ID: 2311006, Kind: skills.KindBuff, Cast: buff("BOOSTER", "x")})                 // Magic Booster
	registerTeleportMastery(2311007)                                                                                // Teleport Mastery
	skills.Register(skills.Handler{ID: 2311009, Kind: skills.KindPartyBuff, Cast: party(holyMagicShell)})           // Holy Magic Shell

	passive(2320011, arcaneAim)                                                                                       // Arcane Aim
	skills.Register(skills.Handler{ID: 2321000, Kind: skills.KindPartyBuff, Cast: party(buff("MAPLE_WARRIOR", "x"))}) // Maple Warrior
	magic(2321001)                                                                                                    // Big Bang
	skills.Register(skills.Handler{ID: 2321004, Kind: skills.KindBuff, Cast: infinity})                               // Infinity
	skills.Register(skills.Handler{ID: 2321005, Kind: skills.KindPartyBuff, Cast: party(advancedBlessing)})           // Advanced Blessing
	skills.Register(skills.Handler{ID: 2321006, Kind: skills.KindPartyBuff | skills.KindHeal, Cast: resurrection})    // Resurrection
	magic(2321007)                                                                                                    // Angel's Ray
	magic(2321008)                                                                                                    // Genesis
	skills.Register(skills.Handler{ID: 2321009, Kind: skills.KindSpecial, Cast: skills.HerosWill})                    // Hero's Will
	passive(2321010, buffMastery)                                                                                     // Buff Mastery
}

// holyFocus (2310008) adds cr critical rate and ar accuracy percent. With a
// wand or staff its mastery replaces Spell Mastery's: the original v148 cleric
// mastery lookup (sub_6D9733) returns Holy Focus's mastery whenever it is learned.
func holyFocus(c *skills.PassiveContext) skills.Bonus {
	b := skills.Bonus{Critical: c.Values["cr"], ACCPercent: c.Values["ar"]}
	if wandOrStaff(c) {
		b.Mastery = c.Values["mastery"]
	}
	return b
}

// holyMagicShell (2311009) is HOLY_MAGIC_SHELL x with a y second cooldown
// (Java replaces the WZ cooltime) and heals z% HP (Java hpR).
func holyMagicShell(c *skills.CastContext) (skills.CastPlan, error) {
	p, e := buff("HOLY_MAGIC_SHELL", "x")(c)
	p.Cooldown = time.Duration(c.Value("y")) * time.Second
	p.Heal = &skills.Heal{HPPercent: c.Value("z")}
	return p, e
}

// advancedBlessing (2321005) is HOLY_SHIELD x (Java's non-GMS value) plus the
// generic indieMhp/indieMmp statups HP_BOOST and MP_BOOST.
func advancedBlessing(c *skills.CastContext) (skills.CastPlan, error) {
	p, e := buff("HOLY_SHIELD", "x")(c)
	if e == nil {
		p.Self.Stats = append(p.Self.Stats, skills.StatValue{Stat: "HP_BOOST", Value: c.Value("indieMhp")}, skills.StatValue{Stat: "MP_BOOST", Value: c.Value("indieMmp")})
	}
	return p, e
}

// resurrection (2321006) revives the dead party members in range with full
// HP (Java isResurrection), paying mpCon and cooltime.
func resurrection(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Revive = true
	return skills.PartyPlan(c, &skills.Buff{}, p)
}
