package thief

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Assassin (410), Hermit (411), Night Lord (412).
func init() {
	skills.Register(skills.Handler{ID: 4100000, Kind: skills.KindPassive, Passive: clawMastery})
	skills.Register(skills.Handler{ID: 4100001, Kind: skills.KindPassive, Passive: criticalThrow})
	skills.Register(skills.Handler{ID: 4100006, Kind: skills.KindPassive, Passive: shadowResistance})
	skills.Register(skills.Handler{ID: 4101003, Kind: skills.KindBuff, Cast: booster})
	skills.Register(skills.Handler{ID: 4101004, Kind: skills.KindPartyBuff, Cast: skills.StandardPartyBuff})                                                       // Haste: speed, jump
	skills.Register(skills.Handler{ID: 4101005, Kind: skills.KindAttack | skills.KindHeal, Attack: shoot(noAmmo, func(s *combat.AttackSpec) { s.Drain = drain })}) // Drain: the native client sends it as a shoot attack; Legacy consumes no star (bulletCount 0)

	skills.Register(skills.Handler{ID: 4110000, Kind: skills.KindPassive, Passive: alchemist})
	skills.Register(skills.Handler{ID: 4111001, Kind: skills.KindPartyBuff, Cast: mesoUp})
	skills.Register(skills.Handler{ID: 4111002, Kind: skills.KindBuff, Cast: shadowPartner}) // itemCon 4006001 via StandardCost
	// v148 sends Shadow Web in a 0x55 attack frame. The WZ defines both the
	// initial hit and the web's damage ticks.
	skills.Register(skills.Handler{ID: 4111003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(shadowWeb))})
	skills.Register(skills.Handler{ID: 4111004, Kind: skills.KindAttack, Attack: shoot(noAmmo, func(s *combat.AttackSpec) { s.DamageRange = shadowMesoRange })}) // Shadow Meso: moneyCon
	skills.Register(skills.Handler{ID: 4111005, Kind: skills.KindAttack, Attack: shoot()})                                                                       // Avenger: bulletConsume 3
	skills.Register(skills.Handler{ID: 4111006, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                                                // Flash Jump
	skills.Register(skills.Handler{ID: 4111007, Kind: skills.KindSummon, Cast: summonBuff, Attack: darkFlare()})                                                 // Dark Flare

	skills.Register(skills.Handler{ID: 4120002, Kind: skills.KindPassive, Passive: shadowShifter})
	skills.Register(skills.Handler{ID: 4120005, Kind: skills.KindPassive | skills.KindMobStatus}) // Venomous Star: POISON on venom attacks
	skills.Register(skills.Handler{ID: 4120010, Kind: skills.KindPassive})                        // Expert Throwing Star Handling: damR, prop keeps the star
	skills.Register(skills.Handler{ID: 4121000, Kind: skills.KindPartyBuff, Cast: mapleWarrior})
	skills.Register(skills.Handler{ID: 4121003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(noAmmo, hit(taunt...))}) // Taunt: AOE without stars
	skills.Register(skills.Handler{ID: 4121004, Kind: skills.KindMobStatus, Cast: costOnly, Attack: mobCast(ambush)})                  // Ninja Ambush
	skills.Register(skills.Handler{ID: 4121006, Kind: skills.KindBuff, Cast: shadowStars})
	skills.Register(skills.Handler{ID: 4121007, Kind: skills.KindAttack, Attack: shoot()})                                 // Triple Throw: LUK-based ceiling
	skills.Register(skills.Handler{ID: 4121008, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(stun))}) // Ninja Storm: knockback
	skills.Register(skills.Handler{ID: 4121009, Kind: skills.KindSpecial, Cast: skills.HerosWill})                         // Hero's Will
}

// clawMastery (4100000) adds mastery and x accuracy with a claw (Java
// CalcPassive_Mastery).
func clawMastery(c *skills.PassiveContext) skills.Bonus {
	if weaponType(c.Character) != weaponClaw {
		return skills.Bonus{}
	}
	return basicWeaponMastery(c, 4120012)
}

// criticalThrow (4100001, 4340010) adds prop critical rate and raises the
// minimum critical damage by criticaldamageMin; the maximum is unchanged.
func criticalThrow(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{Critical: c.Values["prop"], CriticalDamageMin: c.Values["criticaldamageMin"]}
}

// shadowResistance (4100006, 4200006, 4310004) adds mhpR percent max HP and
// the asrR/terR resistances.
func shadowResistance(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MaxHPPercent: c.Values["mhpR"], ASR: c.Values["asrR"], TER: c.Values["terR"]}
}

// alchemist (4110000) raises potion recovery by x-100 percent (Java
// RecoveryUP); BuffUP y-100 has no Bonus field.
func alchemist(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{PotionPercent: c.Values["x"] - 100}
}

// shadowShifter (4120002, 4220002) dodges a hit with prop percent.
func shadowShifter(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{AvoidRate: c.Values["prop"]}
}

// booster (4101003, 4201002, 4301002) is BOOSTER x (negative attack speed).
func booster(c *skills.CastContext) (skills.CastPlan, error) {
	return selfBuff(c, stat("BOOSTER", c.Value("x")))
}

// mesoUp (4111001) is a party MESOUP x.
func mesoUp(c *skills.CastContext) (skills.CastPlan, error) {
	return partyBuff(c, stat("MESOUP", c.Value("x")))
}

// shadowPartner (4111002) is SHADOWPARTNER x; StandardCost takes itemConNo
// of itemCon (Summoning Rock 4006001).
func shadowPartner(c *skills.CastContext) (skills.CastPlan, error) {
	return selfBuff(c, stat("SHADOWPARTNER", c.Value("x")))
}

// mapleWarrior (4121000, 4221000, 4341000) is a party MAPLE_WARRIOR x.
func mapleWarrior(c *skills.CastContext) (skills.CastPlan, error) {
	return partyBuff(c, stat("MAPLE_WARRIOR", c.Value("x")))
}

// shadowStars (4111009, 4121006) sets SPIRIT_CLAW to the throwing-star index:
// v148 CUserLocal::FindAmmo (0x8EF0AA..0x8EF0F6) requires SPIRIT_CLAW > 0 and
// resolves the star item ID as SPIRIT_CLAW + 2069999 (so 2070000 encodes as 1).
// Legacy does not consume bulletConsume at cast.
func shadowStars(c *skills.CastContext) (skills.CastPlan, error) {
	val := 1
	bestSlot := 0
	for _, item := range c.Caster.Items {
		if item.InvType == 2 && item.Slot > 0 && item.Quantity > 0 &&
			combat.AmmoOf(item.TemplateID) == combat.AmmoStar && item.TemplateID > 2069999 &&
			(bestSlot == 0 || item.Slot < bestSlot) {
			bestSlot = item.Slot
			val = item.TemplateID - 2069999
		}
	}
	return selfBuff(c, stat("SPIRIT_CLAW", val))
}
