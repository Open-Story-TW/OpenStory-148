package pirate

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// The regional v148 books (509, 58x, 59x) repeat classic pirate skills under
// their own ids. The sender of each follows the v148 active-skill dispatcher
// sub_93694C: 0x938BFF calls the 0x55 close-range sender sub_92D0E9,
// 0x93920F the 0x56 ranged sender sub_92D2E4, 0x93AEE1 the key-down sender
// sub_93472C, 0x939473 the summon sender sub_92E03B and 0x93891D the 0xA3
// skill-use sender sub_931C46.
func init() {
	for _, a := range []attack{
		{id: 5091001, kind: combat.Melee}, // 衝擊拳 (0x9396A8 → 0x938BFF), as 5001001
		{id: 5091002, kind: combat.Melee}, // 旋風斬 (0x9396A8 → 0x938BFF), as 5001002
		// 衝擊波 (0x939A76 → 0x938BFF): WZ requireSkill 5811005||5821003, the transformations.
		{id: 5811006, kind: combat.Melee, prepare: requiresMorph(5811005, 1000, 5821003, 1001)},
		{id: 5821001, kind: combat.Melee}, // 魔龍降臨 (0x939AD2 → 0x938BFF), as 5121001
		// 元氣彈 (0x9392FB → 0x93920F) needs a full gauge, like 5811002.
		{id: 5821002, kind: combat.Shoot, noAmmo: true, prepare: requiresAttackBuff(5810001, "ENERGY_CHARGE", energyFull)},
		// 閃．索命 (0x939B1D → 0x938BFF): WZ requireSkill 5821003 and mes stun, as 5121005.
		{id: 5821005, kind: combat.Melee, onHit: stun, prepare: requiresAttackBuff(5821003, "MORPH", 1001)},
		{id: 5821007, kind: combat.Melee},                                // 閃．連殺 (0x939B1D → 0x938BFF), as 5121007
		{id: 5901001, kind: combat.Shoot},                                // 散射 (0x93995F → 0x93920F), bulletConsume 1, as 5201001
		{id: 5911004, kind: combat.Shoot, prepare: regionalFlamethrower}, // 火焰噴射 (0x939BAD → 0x93920F), as 5211004
		{id: 5920011, kind: combat.Shoot, onAttack: homingBeacon},        // 精準砲擊 (0x939B7D → 0x93920F), as 5220011
		{id: 5921003, kind: combat.Melee},                                // 海鷗特戰隊 (0x939BE0 → 0x938BFF)
		{id: 5921004, kind: combat.Shoot},                                // 瞬‧迅雷 (0x93A6FD → 0x93AEE1, key-down), as 5221004
	} {
		kind := skills.KindAttack
		if a.onHit != nil {
			kind |= skills.KindMobStatus
		}
		skills.Register(skills.Handler{ID: a.id, Kind: kind, Attack: spec(a), OnAttack: a.onAttack})
	}
	// 海鷗突擊隊 (0x939B95 → 0x939473) is the summon of 5211002.
	gaviota := summon{id: 5911002, stat: "SUMMON", move: circleStationary, multi: true, single: true, mobs: 3}
	skills.Register(skills.Handler{ID: gaviota.id, Kind: skills.KindSummon, Cast: valueOne(gaviota.stat),
		Attack: &combat.AttackSpec{Kind: combat.Summoned, Prepare: summonTargets(gaviota.mobs),
			Summon: &combat.SummonSpec{MoveAbility: gaviota.move, AssistType: 1, Multi: gaviota.multi, SingleAttack: gaviota.single}}})
	// The three casts go through 0xA3: 魔力再生 (0x939A68 → 0x93891D) is
	// 5101005's MP Recovery with WZ x 10, 偽裝術 (0x939A83) is Oak Barrel's
	// morph 1002 and 幸運骰子 (0x939AC0) rolls like the other regional dice.
	skills.Register(skills.Handler{ID: 5801005, Kind: skills.KindHeal, Cast: mpRecovery})
	skills.Register(skills.Handler{ID: 5801007, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1002, false)})
	skills.Register(skills.Handler{ID: 5811007, Kind: skills.KindBuff, Cast: dice(6)})
	// 瞬‧冰火連擊 raises the regional Flamethrower and Ice Splitter by WZ
	// damage; the damage calculator reads it at 0x712C46, as 5220001.
	skills.Register(skills.Handler{ID: 5920001, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{SkillDamage: skillDamage(5911004, c.Values["damage"], 5911005, c.Values["damage"])}
	}})
}

// requiresMorph accepts the attack while either transformation is active.
func requiresMorph(a, aMorph, b, bMorph int) func(*combat.AttackContext) error {
	first, second := requiresAttackBuff(a, "MORPH", aMorph), requiresAttackBuff(b, "MORPH", bMorph)
	return func(c *combat.AttackContext) error {
		if first(c) == nil {
			return nil
		}
		return second(c)
	}
}

// regionalFlamethrower is fire (WZ elemAttr f) with 5211004's burn.
func regionalFlamethrower(c *combat.AttackContext) error {
	if err := regionalFireElement(c); err != nil {
		return err
	}
	return poison(c)
}
