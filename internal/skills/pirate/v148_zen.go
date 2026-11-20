package pirate

import (
	"fmt"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Regional 57xx attacks the v148 client sends but no handler covered. The
// family follows the native sender that references each id: the 0x55
// melee sender 0x91A08F for Panda Kill and Dragon Fist, the shoot sender
// 0x90B36D (and the gun dispatcher 0x92D2E4 for Tiger Charge) for the
// others. None of them lists a bulletCount, so no bullet is consumed.
func init() {
	for _, a := range []attack{
		{id: 5701001, kind: combat.Melee},               // 熊貓殺 Panda Kill
		{id: 5711006, kind: combat.Melee},               // 龍拳 Dragon Fist
		{id: 5711002, kind: combat.Shoot, noAmmo: true}, // 猛虎衝 Tiger Charge
		// The x% explosion after Tiger Charge (its w); both senders reference it.
		{id: 5710012, kind: combat.Melee | combat.Shoot, noAmmo: true, levelFrom: 5711002},
		{id: 5721004, kind: combat.Shoot, noAmmo: true},                            // 蒼龍燎原 Azure Dragon
		{id: 5721006, kind: combat.Shoot, noAmmo: true, maxDamage: dragonSoulLine}, // 龍魂一擊 Dragon Soul Strike
		// 精準砲擊: the v148 client lists 5721003 with 5221015/5920011 as the
		// marking shot (0x6AA744) and reads its x against the marked monster
		// (0x6F312E), so it marks like 5920011.
		{id: 5721003, kind: combat.Shoot, onAttack: homingBeacon},
	} {
		skills.Register(skills.Handler{ID: a.id, Kind: skills.KindAttack, Attack: spec(a), OnAttack: a.onAttack, LevelFrom: a.levelFrom})
	}
	// 破城砲 Siege Cannon: the v148 skill dispatcher sends 5711001 to the
	// stationary summon path at 0x939473 (sub_92E03B), the one 5911001 and
	// 5920002 use.
	skills.Register(skills.Handler{
		ID: 5711001, Kind: skills.KindSummon, Cast: valueOne("PUPPET"),
		Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 1, Multi: true}},
	})
	// 聚精會神 Focus: indiePad attack and acc accuracy for the WZ time.
	skills.Register(skills.Handler{ID: 5701006, Kind: skills.KindBuff, Cast: zenBuff(zenStat{"ANGEL_ATK", "indiePad"}, zenStat{"ACC", "acc"})})
	// 海盜風采 Pirate's Style: damR, x% abnormal and y% elemental resistance,
	// z% knockback resistance and eva (negative) avoidability.
	skills.Register(skills.Handler{ID: 5721009, Kind: skills.KindBuff, Passive: pirateStyleDamage, Cast: zenBuff(
		zenStat{"PIRATES_REVENGE", "damR"}, zenStat{"ABNORMAL_STATUS_R", "x"}, zenStat{"ELEMENTAL_STATUS_R", "y"},
		zenStat{"STANCE", "z"}, zenStat{"AVOID", "eva"})})
}

type zenStat struct {
	stat buffstat.Stat
	key  string
}

// zenBuff is a self buff of the listed WZ values for the WZ time; keys with
// a zero value are left out.
func zenBuff(stats ...zenStat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d, err := buffTime(c)
		if err != nil {
			return p, err
		}
		b := &skills.Buff{Duration: d}
		for _, s := range stats {
			if v := c.Value(s.key); v != 0 {
				b.Stats = append(b.Stats, skills.StatValue{Stat: s.stat, Value: v})
			}
		}
		if len(b.Stats) == 0 {
			return p, fmt.Errorf("skill%d: no stats", c.Skill.ID)
		}
		p.Self = b
		return p, nil
	}
}

// pirateStyleDamage is Pirate's Style's damR while the buff runs; the v148
// client adds the PIRATES_REVENGE (bit 116) value to its damage percent sum.
func pirateStyleDamage(c *skills.PassiveContext) skills.Bonus {
	if !c.Active(5721009) {
		return skills.Bonus{}
	}
	return skills.Bonus{DamagePercent: c.Values["damR"]}
}

// dragonSoulLine (5721006) follows Snipe (3221007) in the v148 physical
// calc 0x71160B: against a non-boss each line is 989999 plus a roll up to
// 10000; against a boss the WZ cr is 100, so every line is critical.
func dragonSoulLine(c combat.DamageContext) int {
	if !c.Mob.Boss && !c.Mob.Invincible {
		return combat.MaxLineDamage()
	}
	return combat.CritCeiling(c, combat.LineCeiling(c))
}
