package thief

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Bandit (420), Chief Bandit (421), Shadower (422).
func init() {
	skills.Register(skills.Handler{ID: 4200000, Kind: skills.KindPassive, Passive: daggerMastery})
	skills.Register(skills.Handler{ID: 4200006, Kind: skills.KindPassive, Passive: shadowResistance})
	skills.Register(skills.Handler{ID: 4201002, Kind: skills.KindBuff, Cast: booster})
	skills.Register(skills.Handler{ID: 4201003, Kind: skills.KindPartyBuff, Cast: skills.StandardPartyBuff})                                                                                          // Haste: speed, jump
	skills.Register(skills.Handler{ID: 4201004, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(stun), func(s *combat.AttackSpec) { s.AfterHit = chainAfter(s.AfterHit, steal) })}) // Steal
	skills.Register(skills.Handler{ID: 4201005, Kind: skills.KindAttack, Attack: melee(pickpocketOn)})                                                                                                // Savage Blow

	skills.Register(skills.Handler{ID: 4210000, Kind: skills.KindPassive, Passive: shieldMastery})
	skills.Register(skills.Handler{ID: 4211001, Kind: skills.KindHeal, Cast: chakra})
	skills.Register(skills.Handler{ID: 4211002, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(pickpocketOn, hit(stun))}) // Assaulter
	skills.Register(skills.Handler{ID: 4211003, Kind: skills.KindBuff, Cast: pickpocket})
	skills.Register(skills.Handler{ID: 4211004, Kind: skills.KindAttack, Attack: melee(pickpocketOn)}) // Band of Thieves
	skills.Register(skills.Handler{ID: 4211005, Kind: skills.KindBuff, Cast: mesoGuard})
	skills.Register(skills.Handler{ID: 4211006, Kind: skills.KindAttack, Attack: melee(func(s *combat.AttackSpec) { s.ConsumeMesos, s.MaxDamage = true, mesoExplosionCeiling })}) // Meso Explosion
	skills.Register(skills.Handler{ID: 4211007, Kind: skills.KindSummon, Cast: summonBuff, Attack: darkFlare()})                                                                  // Dark Flare
	skills.Register(skills.Handler{ID: 4211008, Kind: skills.KindBuff, Cast: banditShadowPartner})
	skills.Register(skills.Handler{ID: 4211009, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // Flash Jump

	skills.Register(skills.Handler{ID: 4220002, Kind: skills.KindPassive, Passive: shadowShifter})
	skills.Register(skills.Handler{ID: 4220005, Kind: skills.KindPassive | skills.KindMobStatus}) // Venomous Stab
	skills.Register(skills.Handler{ID: 4220009, Kind: skills.KindPassive, Passive: greed})        // pickRate u, Meso Guard v/w; Meso Explosion +x in boosted
	skills.Register(skills.Handler{ID: 4221000, Kind: skills.KindPartyBuff, Cast: mapleWarrior})
	skills.Register(skills.Handler{ID: 4221001, Kind: skills.KindAttack, Attack: melee()})                                                           // Assassinate
	skills.Register(skills.Handler{ID: 4221003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: shoot(pickpocketOn, noAmmo, hit(taunt...))}) // Taunt
	skills.Register(skills.Handler{ID: 4221004, Kind: skills.KindMobStatus, Cast: costOnly, Attack: mobCast(ambush)})                                // Ninja Ambush
	skills.Register(skills.Handler{ID: 4221006, Kind: skills.KindFieldObject, Cast: costOnly, Attack: smokeScreen})                                  // Smoke Screen
	skills.Register(skills.Handler{ID: 4221007, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(pickpocketOn, hit(stun))})             // Boomerang Step: damR for Savage Blow/Steal/Assaulter
	skills.Register(skills.Handler{ID: 4221008, Kind: skills.KindSpecial, Cast: skills.HerosWill})                                                   // Hero's Will
}

// daggerMastery (4200000) adds mastery and x accuracy with a dagger, for
// non-Dual Blade jobs (Java CalcPassive_Mastery).
func daggerMastery(c *skills.PassiveContext) skills.Bonus {
	if w := weaponType(c.Character); w != weaponDagger && w != weaponKatara || dualBlade(c.Character.Job) {
		return skills.Bonus{}
	}
	return skills.Bonus{Mastery: c.Values["mastery"], ACC: c.Values["x"]}
}

// pickpocket (4211003) is PICKPOCKET x: normal and listed attacks drop mesos.
func pickpocket(c *skills.CastContext) (skills.CastPlan, error) {
	return selfBuff(c, stat("PICKPOCKET", c.Value("x")))
}

// mesoGuard (4211005) is MESOGUARD x: damage taken is paid partly in mesos.
func mesoGuard(c *skills.CastContext) (skills.CastPlan, error) {
	return selfBuff(c, stat("MESOGUARD", c.Value("x")))
}

// banditShadowPartner (4211008) is SHADOWPARTNER with the skill level as its
// value (Java), not x; the extra hit still uses x. StandardCost takes the
// Summoning Rock.
func banditShadowPartner(c *skills.CastContext) (skills.CastPlan, error) {
	return selfBuff(c, stat("SHADOWPARTNER", c.Level))
}

// shieldMastery (4210000) adds x percent WDEF and MDEF (Java PlayerStats for
// 420–422, no shield check).
func shieldMastery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{WDEFPercent: c.Values["x"], MDEFPercent: c.Values["x"]}
}

// greed (4220009) raises the meso drop rate by mesoR percent.
func greed(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MesoRate: c.Values["mesoR"]}
}

// chakra (4211001) heals the caster by Java calcHPChange: (rand(101..200) ×
// LUK × 0.033 + DEX) × (y+100) × 0.002 plus a LUK roll between 2.3 and 3.5
// times y percent, with equipped LUK/DEX.
func chakra(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	luk, dex := c.Caster.LUK, c.Caster.DEX
	for _, e := range c.Caster.Equipment {
		if e.Slot < 0 && e.Slot > -100 {
			luk += e.LUK
			dex += e.DEX
		}
	}
	y := float64(c.Value("y"))
	hp := int((float64(100+rollRange(1, 100))*float64(luk)*0.033 + float64(dex)) * (y + 100) * 0.002)
	lo, hi := int(float64(luk)*2.3*y/100), int(float64(luk)*3.5*y/100)
	hp += lo + rollRange(0, hi-lo)
	p.Heal = &skills.Heal{HP: max(hp, 1)}
	return p, nil
}
