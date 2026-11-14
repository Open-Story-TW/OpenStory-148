package magician

import "maple-go-server/internal/skills"

// Wizard (F/P) 210, Mage (F/P) 211, Arch Mage (F/P) 212.
func init() {
	passive(2100000, nil)                                                                                      // MP Eater: attack-pipeline proc
	passive(2100006, spellMastery)                                                                             // Spell Mastery
	passive(2100007, highWisdom)                                                                               // High Wisdom
	skills.Register(skills.Handler{ID: 2101001, Kind: skills.KindPartyBuff, Cast: party(skills.StandardBuff)}) // Meditation: mad
	skills.Register(skills.Handler{ID: 2101002, Kind: skills.KindSpecial, Cast: skills.CostOnly})              // Teleport
	magic(2101004)                                                                                             // Fire Arrow
	magic(2101005)                                                                                             // Poison Breath: dot

	passive(2110000, elementalDrainFP)                                                                    // Elemental Drain
	passive(2110001, elementAmplification)                                                                // Element Amplification
	passive(2110009, criticalPassive)                                                                     // Critical passive: cr, criticaldamageMin
	magic(2111002)                                                                                        // Explosion
	skills.Register(skills.Handler{ID: 2111005, Kind: skills.KindBuff, Cast: buff("BOOSTER", "x")})       // Spell Booster
	magic(2111006)                                                                                        // Element Composition (F/P): dot
	registerTeleportMastery(2111007)                                                                      // Teleport Mastery
	skills.Register(skills.Handler{ID: 2111008, Kind: skills.KindBuff, Cast: buff("ELEMENT_RESET", "x")}) // Elemental Reset

	passive(2120010, arcaneAim)                                                                                       // Arcane Aim
	skills.Register(skills.Handler{ID: 2121000, Kind: skills.KindPartyBuff, Cast: party(buff("MAPLE_WARRIOR", "x"))}) // Maple Warrior
	magic(2121001)                                                                                                    // Big Bang: dot
	skills.Register(skills.Handler{ID: 2121004, Kind: skills.KindBuff, Cast: infinity})                               // Infinity
	magic(2121006, freeze)                                                                                            // Paralyze: dot
	magic(2121007)                                                                                                    // Meteor Shower: dot
	skills.Register(skills.Handler{ID: 2121008, Kind: skills.KindSpecial, Cast: skills.HerosWill})                    // Hero's Will
	passive(2121009, buffMastery)                                                                                     // Buff Mastery
}
