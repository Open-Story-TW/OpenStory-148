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
	if damage <= 0 {
		return 0, 0
	}
	// Combo Barrier and Magic Shield keep value/1000 of the hit (Java).
	for _, stat := range []buffstat.Stat{"COMBO_BARRIER", "MAGIC_SHIELD"} {
		if v, ok := ActiveStat(c, stat, now); ok && v > 0 && v < 1000 {
			damage = damage * v / 1000
		}
	}
	// Damage reduction: passive Bonus.DamageReduce plus DAMAGE_TAKEN_BUFF,
	// Satellite Safe's absorb percent (35121006) and WATER_SHIELD.
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
	if reduce > 0 {
		damage -= damage * min(reduce, 100) / 100
	}
	guard, ok := ActiveStat(c, "MAGIC_GUARD", now)
	if !ok || guard <= 0 {
		return damage, 0
	}
	mp = damage * min(guard, 100) / 100
	hp = damage - mp
	if mp > c.MP {
		hp += mp - c.MP
		mp = c.MP
	}
	return hp, mp
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
