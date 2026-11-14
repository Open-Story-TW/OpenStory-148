package magician

import "maple-go-server/internal/skills"

// Wizard (I/L) 220, Mage (I/L) 221, Arch Mage (I/L) 222.
func init() {
	passive(2200000, nil)                                                                                      // MP Eater: attack-pipeline proc
	passive(2200006, spellMastery)                                                                             // Spell Mastery
	passive(2200007, highWisdom)                                                                               // High Wisdom
	skills.Register(skills.Handler{ID: 2201001, Kind: skills.KindPartyBuff, Cast: party(skills.StandardBuff)}) // Meditation: mad
	skills.Register(skills.Handler{ID: 2201002, Kind: skills.KindSpecial, Cast: skills.CostOnly})              // Teleport
	magic(2201004, freeze)                                                                                     // Cold Beam
	magic(2201005)                                                                                             // Thunder Bolt

	passive(2210000, elementalDrainIL)                                                                    // Elemental Drain
	passive(2210001, elementAmplification)                                                                // Element Amplification
	passive(2210009, criticalPassive)                                                                     // Critical passive: cr, criticaldamageMin
	magic(2211002, freeze)                                                                                // Ice Strike
	magic(2211003, stun)                                                                                  // Thunder Spear
	skills.Register(skills.Handler{ID: 2211005, Kind: skills.KindBuff, Cast: buff("BOOSTER", "x")})       // Spell Booster
	magic(2211006, freeze)                                                                                // Element Composition (I/L): dot
	registerTeleportMastery(2211007)                                                                      // Teleport Mastery
	skills.Register(skills.Handler{ID: 2211008, Kind: skills.KindBuff, Cast: buff("ELEMENT_RESET", "x")}) // Elemental Reset

	passive(2220010, arcaneAim)                                                                                       // Arcane Aim
	skills.Register(skills.Handler{ID: 2221000, Kind: skills.KindPartyBuff, Cast: party(buff("MAPLE_WARRIOR", "x"))}) // Maple Warrior
	magic(2221001, freeze)                                                                                            // Big Bang
	magic(2221003, freeze)                                                                                            // Ice Demon: dot
	skills.Register(skills.Handler{ID: 2221004, Kind: skills.KindBuff, Cast: infinity})                               // Infinity
	magic(2221006, stun)                                                                                              // Chain Lightning
	magic(2221007, freeze)                                                                                            // Blizzard: dot
	skills.Register(skills.Handler{ID: 2221008, Kind: skills.KindSpecial, Cast: skills.HerosWill})                    // Hero's Will
	passive(2221009, buffMastery)                                                                                     // Buff Mastery
}
