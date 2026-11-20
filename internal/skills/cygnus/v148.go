package cygnus

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

func init() {
	// Night Walker's v148 book uses the common WZ movement buff and the same
	// ranged star and poison rules as its other throwing-star attacks.
	skills.Register(skills.Handler{ID: 14001007, Kind: skills.KindPartyBuff | skills.KindPassive, Cast: party(generic), Passive: skills.MovementPassive})
	skills.Register(skills.Handler{ID: 14101008, Kind: skills.KindAttack, Attack: nightShoot()})
	skills.Register(skills.Handler{ID: 14101009, Kind: skills.KindAttack, Attack: nightShoot()})
	passive(14110009, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{MaxHPPercent: c.Values["mhpR"], ASR: c.Values["asrR"], TER: c.Values["terR"]}
	})
	passive(14110011, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{PotionPercent: c.Values["x"] - 100}
	})

	passive(15000006, critical("cr"))
	passive(15000008, maxHP)
	skills.Register(skills.Handler{ID: 15101008, Kind: skills.KindBuff, Cast: generic}) // 全神貫注: acc and indiePad
	skills.Register(skills.Handler{ID: 15101010, Kind: skills.KindAttack, Attack: thunderMelee(prepare(tornadoUpper))})
	skills.Register(skills.Handler{ID: 15111008, Kind: skills.KindAttack | skills.KindPassive,
		Attack: attack(combat.Shoot, noAmmo, prepare(energyBlastReady), prepare(thunderCharged))})
	skills.Register(skills.Handler{ID: 15111012, Kind: skills.KindAttack, Attack: thunderMelee()})
}

// tornadoUpper extends the projectile's local v148 WZ box by its horizontal
// travel distance. The native melee frame reports the caster position.
func tornadoUpper(ctx *combat.AttackContext) error {
	if ctx.Effect.Box.Set && ctx.Effect.X > 0 {
		ctx.Effect.Box.LT.X -= ctx.Effect.X
		ctx.Effect.Box.RB.X += ctx.Effect.X
	}
	return nil
}

// v148 0x92D406 dispatches Energy Blast through the energy gauge readiness
// getter (0x6F1954) before the native 0x56 physical attack sender.
func energyBlastReady(ctx *combat.AttackContext) error {
	weapon := ctx.Stats.WeaponID / 10_000 % 100
	if weapon != 48 && weapon != 39 {
		return combat.ErrRejected
	}
	for _, row := range ctx.Character.SkillBuffs {
		if row.ID == 15100004 && row.Stat == "ENERGY_CHARGE" && row.Value >= energyFull && row.ExpiresAt.After(ctx.Now) {
			return nil
		}
	}
	return combat.ErrRejected
}
