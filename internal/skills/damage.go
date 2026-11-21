package skills

import (
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"time"
)

// TakenDamage splits monster damage between HP and MP like Java
// MapleCharacter damage handling: MAGIC_GUARD moves x percent onto MP, and
// whatever MP cannot pay falls back to HP.
func TakenDamage(c domain.Character, damage int, now time.Time) (hp, mp int) {
	hp, mp, _ = TakenDamageWithMesos(c, damage, now)
	return hp, mp
}

// TakenDamageWithMesos computes HP, MP and meso loss from a monster hit,
// applying Combo Barrier, Magic Shield, DamageReduce, Power Guard, Meso Guard
// (50% HP absorption paid at x% in mesos) and Magic Guard.
func TakenDamageWithMesos(c domain.Character, damage int, now time.Time) (hp, mp int, mesos int64) {
	if damage <= 0 {
		return 0, 0, 0
	}
	// Combo Barrier and Magic Shield keep value/1000 of the hit (Java).
	for _, stat := range []buffstat.Stat{"COMBO_BARRIER", "MAGIC_SHIELD"} {
		if v, ok := ActiveStat(c, stat, now); ok && v > 0 && v < 1000 {
			damage = damage * v / 1000
		}
	}
	// Damage reduction: passive Bonus.DamageReduce plus DAMAGE_TAKEN_BUFF,
	// Satellite Safe's absorb percent (35121006), WATER_SHIELD and POWERGUARD.
	reduce := Bonuses(c, now).DamageReduce
	if v, ok := ActiveStat(c, "DAMAGE_TAKEN_BUFF", now); ok {
		reduce += v
	}
	// Satellite Safe absorbs only a hit of at least PROC percent of MaxHP
	// while the satellite (PUPPET) is out (Java MapleCharacter 12246).
	if v, ok := ActiveStat(c, "SATELLITESAFE_ABSORB", now); ok {
		proc, _ := ActiveStat(c, "SATELLITESAFE_PROC", now)
		if _, puppet := ActiveStat(c, "PUPPET", now); puppet && damage*100 >= c.MaxHP*proc {
			reduce += v
		}
	}
	if v, ok := ActiveStat(c, "WATER_SHIELD", now); ok {
		reduce += v // Mercedes Water Shield absorbs x percent.
	}
	if v, ok := ActiveStat(c, "POWERGUARD", now); ok && v > 0 {
		reduce += v // Power Guard (1101007/1201007/11101006) absorbs x percent.
	}
	if reduce > 0 {
		damage -= damage * min(reduce, 100) / 100
	}
	if rate, ok := ActiveStat(c, "MESOGUARD", now); ok && rate > 0 && damage > 1 && c.Mesos > 0 {
		absorbed := damage / 2
		cost := int64(absorbed) * int64(rate) / 100
		if cost > c.Mesos {
			absorbed = int(c.Mesos * 100 / int64(rate))
			cost = int64(absorbed) * int64(rate) / 100
		}
		if absorbed > 0 {
			damage -= absorbed
			mesos = cost
		}
	}
	guard, ok := ActiveStat(c, "MAGIC_GUARD", now)
	if !ok || guard <= 0 {
		return damage, 0, mesos
	}
	mp = damage * min(guard, 100) / 100
	hp = damage - mp
	if mp > c.MP {
		hp += mp - c.MP
		mp = c.MP
	}
	return hp, mp, mesos
}

// PowerGuardReflect is the OnDamaged reflection handler for Power Guard
// (1101007, 1201007, 11101006): reflects x% * y% of raw contact damage back
// to the attacking monster, capped at 10% of the monster's MaxHP.
func PowerGuardReflect(c *TriggerContext) *BuffChange {
	pg, ok := c.Stat("POWERGUARD")
	if !ok || pg <= 0 || c.Damage <= 0 || c.Mob == 0 {
		return nil
	}
	y := c.Value("y")
	if y <= 0 {
		y = 100
	}
	bounce := int(int64(c.Damage) * int64(pg) * int64(y) / 10000)
	if m, ok := c.Attacker(); ok && m.MaxHP > 0 {
		bounce = int(min(int64(bounce), max(1, m.MaxHP/10)))
	}
	if bounce < 1 {
		return nil
	}
	return &BuffChange{MobHits: []combat.ServerHit{{ObjectID: c.Mob, Damage: bounce}}}
}

// NormalAttack is the ordinary-attack spec: no projectile is consumed while
// Soul Arrow or Shadow Stars (SPIRIT_CLAW) is active.
var NormalAttack = &combat.AttackSpec{Kind: combat.Melee | combat.Shoot, Prepare: func(ctx *combat.AttackContext) error {
	if _, ok := ActiveStat(ctx.Character, "SOULARROW", ctx.Now); ok {
		ctx.NoAmmo = true
	}
	if _, ok := ActiveStat(ctx.Character, "SPIRIT_CLAW", ctx.Now); ok {
		ctx.NoAmmo = true
	}
	if j := ctx.Character.Job; j >= 3500 && j <= 3512 {
		ctx.NoAmmo = true // Mechanic guns use no bullets (Java noBullet).
	}
	return nil
}}
