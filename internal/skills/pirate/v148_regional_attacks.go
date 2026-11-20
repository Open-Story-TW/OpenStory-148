package pirate

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func init() {
	for _, a := range []attack{
		// v148 dispatches the gun skills through sub_92D2E4 (0x56).
		{id: 5081002, kind: combat.Shoot, noAmmo: true}, // native Double Shot sends slot 0 on the regional first job
		{id: 5091003, kind: combat.Shoot},
		{id: 5700004, kind: combat.Shoot, noAmmo: true}, // Triple Shot keeps Double Shot's native slot 0
		{id: 5711000, kind: combat.Shoot, noAmmo: true}, // Double Barrel dispatches 0x56 with slot 0
		{id: 5721001, kind: combat.Shoot, noAmmo: true}, // live: every native frame was refused for ammo (bullet slot 0)
		// The native area shot uses slot 0 even when normal bullets are held.
		{id: 5721007, kind: combat.Shoot, noAmmo: true},
		{id: 5901006, kind: combat.Shoot, noAmmo: true}, // the native frame names bullet slot 0
		{id: 5910000, kind: combat.Shoot},
		{id: 5911005, kind: combat.Shoot, onHit: []combat.MobStatusApply{{Stat: combat.MobFreeze}}, prepare: regionalIceElement},
		{id: 5911006, kind: combat.Shoot, onAttack: homingBeacon},
		{id: 5921007, kind: combat.Shoot, noAmmo: true, prepare: requiresAttackBuff(5921006, "MONSTER_RIDING", battleshipMount)},
		{id: 5921008, kind: combat.Shoot, noAmmo: true, prepare: requiresAttackBuff(5921006, "MONSTER_RIDING", battleshipMount)},
		// Mind Control uses a normal bullet slot and the monster status decoded at v148 0x721BB9.
		{id: 5921009, kind: combat.Shoot, onHit: []combat.MobStatusApply{{Stat: combat.MobDazzle}}, prepare: regionalMindControl},
		// The other skills use the 0x55 sender. Tornado Upper (0x91CA5C)
		// shares its projectile handling with Corsair's 5101012 (0x91CA6C).
		{id: 5081001, kind: combat.Melee, prepare: barrelRoll},
		{id: 5081005, kind: combat.Melee},
		{id: 5701007, kind: combat.Melee}, // Double Spiral is explicit inside 0x91A08F (0x55)
		{id: 5801002, kind: combat.Melee, onHit: stun},
		{id: 5801003, kind: combat.Melee, onHit: stun},
		{id: 5801004, kind: combat.Melee},
		{id: 5811002, kind: combat.Melee, onHit: stun, prepare: requiresAttackBuff(5810001, "ENERGY_CHARGE", energyFull)},
		{id: 5811004, kind: combat.Melee, prepare: requiresAttackBuff(5810001, "ENERGY_CHARGE", energyFull), drain: energyDrain},
		{id: 5821004, kind: combat.Melee, prepare: requiresAttackBuff(5821003, "MORPH", 1001)},
		{id: 5901002, kind: combat.Melee, ignoreRange: true, prepare: regionalFlamethrower},
		{id: 5901004, kind: combat.Melee, onHit: stun},
	} {
		kind := skills.KindAttack
		if a.onHit != nil {
			kind |= skills.KindMobStatus
		}
		skills.Register(skills.Handler{ID: a.id, Kind: kind, Attack: spec(a), OnAttack: a.onAttack})
	}
	// sub_4E5740 includes both key-release skills when the 0x55 sender
	// writes its I32 charge field. Other regional attacks omit the field.
	for _, id := range []int{5801004, 5901002} {
		protocol.RegisterAttackQuirk(id, protocol.AttackQuirk{Charge: true})
	}
	// Both skills dispatch to the same stationary summon path in v148
	// (0x939473) and have their own level, duration, and cooldown.
	for _, id := range []int{5911001, 5920002} {
		skills.Register(skills.Handler{
			ID: id, Kind: skills.KindSummon, Cast: valueOne("PUPPET"),
			Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 1, Multi: true}},
		})
	}
	skills.Register(skills.Handler{ID: 5921006, Kind: skills.KindBuff | skills.KindMorph, Cast: battleship})
}

func requiresAttackBuff(source int, stat string, value int) func(*combat.AttackContext) error {
	return func(c *combat.AttackContext) error {
		for _, b := range c.Character.SkillBuffs {
			if b.ID == source && b.Stat == stat && b.Value == value && b.ExpiresAt.After(c.Now) {
				return nil
			}
		}
		return combat.ErrRejected
	}
}

func regionalFireElement(c *combat.AttackContext) error {
	c.Effect.Element = combat.Fire
	return nil
}

func regionalIceElement(c *combat.AttackContext) error {
	// The original client shoots normal bullets here. Skill.wz's
	// itemConsume refers to an ice capsule absent from v148 Item.wz.
	c.Effect.Element = combat.Ice
	return nil
}

func regionalMindControl(c *combat.AttackContext) error {
	if weaponType(c.Character) != gun {
		return combat.ErrRejected
	}
	return nil
}
