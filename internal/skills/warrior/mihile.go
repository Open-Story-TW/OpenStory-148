package warrior

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"time"
)

func init() {
	skills.Register(skills.Handler{ID: 51000000, Kind: skills.KindPassive, Passive: hpIncrease})
	skills.Register(skills.Handler{ID: 51000001, Kind: skills.KindPassive, Passive: soulShield})
	skills.Register(skills.Handler{ID: 51000002, Kind: skills.KindPassive, Passive: soulAgility})
	skills.Register(skills.Handler{ID: 51001003, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	// The original client sends Soul Blade through the shoot family with no
	// ammo item. The physical damage still uses the equipped sword.
	skills.Register(skills.Handler{ID: 51001004, Kind: skills.KindAttack, Attack: &combat.AttackSpec{Kind: combat.Shoot, Ammo: combat.AmmoNone}})
	skills.Register(skills.Handler{ID: 51100000, Kind: skills.KindPassive, Passive: physicalTraining})
	skills.Register(skills.Handler{ID: 51100001, Kind: skills.KindPassive, Passive: mihileSwordMastery})
	skills.Register(skills.Handler{ID: 51100002, Kind: skills.KindAttack, Attack: melee(nil, mihileFinalAttack)})
	skills.Register(skills.Handler{ID: 51101003, Kind: skills.KindBuff, Cast: mihileSwordBooster})
	skills.Register(skills.Handler{ID: 51101004, Kind: skills.KindPartyBuff, Cast: inspire})
	skills.Register(skills.Handler{ID: 51101005, Kind: skills.KindAttack, Attack: melee(nil)})
	skills.Register(skills.Handler{ID: 51101006, Kind: skills.KindAttack, Attack: melee(nil)})
	skills.Register(skills.Handler{ID: 51110000, Kind: skills.KindPassive, Passive: mpRecovery, OnTick: skills.ReviewedRecoveryTick, TickEvery: 4 * time.Second})
	skills.Register(skills.Handler{ID: 51110001, Kind: skills.KindPassive, Passive: mihileStrength})
	// Righteous Indignation (51110002): v148 0x713106 adds x/y only against status-afflicted targets.
	skills.Register(skills.Handler{ID: 51110002, Kind: skills.KindPassive})
	skills.Register(skills.Handler{ID: 51111003, Kind: skills.KindBuff, Cast: radiantBurst})
	skills.Register(skills.Handler{ID: 51111004, Kind: skills.KindBuff, Cast: soulEndure})
	skills.Register(skills.Handler{ID: 51111005, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: magicCrash()})
	skills.Register(skills.Handler{ID: 51111006, Kind: skills.KindAttack, Attack: melee(nil)})
	// The original 0x56 sender handles both light attacks. Their WZ element
	// still applies, and the equipped sword supplies physical base damage.
	skills.Register(skills.Handler{ID: 51111007, Kind: skills.KindAttack | skills.KindMobStatus, Attack: mihileLightAttack()})
	skills.Register(skills.Handler{ID: 51120000, Kind: skills.KindPassive, Passive: combatMastery})
	skills.Register(skills.Handler{ID: 51120001, Kind: skills.KindPassive, Passive: mihileAdvancedSwordMastery})
	skills.Register(skills.Handler{ID: 51120002, Kind: skills.KindAttack | skills.KindPassive, Attack: melee(nil, mihileFinalAttack), Passive: mihileAdvancedFinalAttack})
	skills.Register(skills.Handler{ID: 51120003, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{DamageReduce: c.Values["t"]}
	}})
	skills.Register(skills.Handler{ID: 51121004, Kind: skills.KindBuff, Cast: value("STANCE", "prop")})
	skills.Register(skills.Handler{ID: 51121005, Kind: skills.KindPartyBuff, Cast: mapleWarrior})
	skills.Register(skills.Handler{ID: 51121006, Kind: skills.KindBuff, Cast: soulRage})
	skills.Register(skills.Handler{ID: 51121007, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee([]combat.MobStatusApply{{
		Stat: combat.MobDarkness, ValueFrom: "x", HalfDurationOnBoss: true,
	}})})
	skills.Register(skills.Handler{ID: 51121008, Kind: skills.KindAttack | skills.KindMobStatus, Attack: mihileLightAttack()})
}

func soulShield(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{WDEF: c.Values["pddX"], MDEF: c.Values["mddX"]}
}

func soulAgility(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{ACC: c.Values["accX"], Speed: c.Values["psdSpeed"], Jump: c.Values["psdJump"]}
}

func mihileSwordMastery(c *skills.PassiveContext) skills.Bonus {
	if weaponType(c.Character) != oneHandedSword {
		return skills.Bonus{}
	}
	// v148 0x6D90DD: Advanced Sword Mastery (51120001) replaces this mastery for 5112.
	return skills.Bonus{Mastery: c.BasicMastery(51120001), ACC: c.Values["accX"]}
}

func mihileStrength(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{STR: c.Values["strX"]}
}

func mihileAdvancedSwordMastery(c *skills.PassiveContext) skills.Bonus {
	if weaponType(c.Character) != oneHandedSword {
		return skills.Bonus{}
	}
	return skills.Bonus{Mastery: c.Values["mastery"], WATK: c.Values["padX"], CriticalDamageMin: c.Values["criticaldamageMin"]}
}

func mihileAdvancedFinalAttack(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{WATK: c.Values["padX"], ACCPercent: c.Values["ar"]}
}

func mihileFinalAttack(c *combat.AttackContext) error {
	if weaponType(c.Character) != oneHandedSword {
		return domain.ErrForbidden
	}
	return nil
}

func mihileLightAttack() *combat.AttackSpec {
	return &combat.AttackSpec{Kind: combat.Shoot, Ammo: combat.AmmoNone, OnHit: stun}
}

func mihileSwordBooster(c *skills.CastContext) (skills.CastPlan, error) {
	if weaponType(c.Caster) != oneHandedSword {
		return skills.CastPlan{}, domain.ErrForbidden
	}
	return buff(c, stat("BOOSTER", c.Value("x")))
}

func radiantBurst(c *skills.CastContext) (skills.CastPlan, error) {
	if weaponType(c.Caster) != oneHandedSword {
		return skills.CastPlan{}, domain.ErrForbidden
	}
	return buff(c, stat("INDIE_DAMAGE", c.Value("indieDamR")))
}

func inspire(c *skills.CastContext) (skills.CastPlan, error) {
	return partyBuff(c, stat("ANGEL_ATK", c.Value("indiePad")))
}

// Soul Endure uses the original v148 x/y/z rates: defense, abnormal and
// elemental resistance. Its original client dispatcher is 0x93B096.
func soulEndure(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("ABNORMAL_STATUS_R", c.Value("y")), stat("ELEMENTAL_STATUS_R", c.Value("z")), stat("DEFENCE_BOOST_R", c.Value("x")))
}

// soulRage (51121006) mirrors Enrage's ENRAGE (x*100+mobCount) and crit buff
// without Hero's combo-orb requirement (v148 0x93B0F0 self buff, WZ cooltime).
func soulRage(c *skills.CastContext) (skills.CastPlan, error) {
	mobCount := c.Value("mobCount")
	if mobCount == 0 {
		mobCount = 1
	}
	return buff(c, stat("ENRAGE", c.Value("x")*100+mobCount), stat("SHARP_EYES", c.Value("z")<<8|c.Value("y")))
}
