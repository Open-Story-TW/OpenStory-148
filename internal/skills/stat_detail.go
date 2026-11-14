package skills

import (
	"math"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// statDetailSources is everything protocol.StatDetail is derived from.
type statDetailSources struct {
	// Bonus is equipment potential + learned passives + active buffs.
	Bonus Bonus
	// DamagePercents and BossDamagePercents are the revealed potential damage
	// lines; each multiplies once (content.PotentialEffects).
	DamagePercents, BossDamagePercents []int
	// HPSteal is the active COMBO_DRAIN percent; DamageReduce is the percent
	// TakenDamage removes from a hit.
	HPSteal, DamageReduce int
	// Server multipliers (domain.ExpRate and friends) and the line cap.
	ExpRate, MesoRate, DropRate, DamageCap int64
}

// StatDetail is the 0x614 view of c: what the server applies for c right now.
//
//   - ExpRate: domain.ExpRate × (100 + Bonus.EXP), the way combat settlement
//     grants mob EXP (RateExperience, then bonusExperience).
//   - DropRate and MesoRate: the server multiplier × 100. Nothing consumes
//     Bonus.MesoRate (Meso Up's mesoR), so it is left out rather than shown.
//   - DamageCap: combat.MaxLineDamage, the long damage cap or the native 999999.
//   - CritDamage: 50 + criticaldamageMax bonuses, the largest critical bonus
//     combat.CritCeiling accepts.
//   - TotalDamage: damR folded with the potential damage lines, as
//     combat's line ceiling does against an ordinary monster. BossDamage is
//     what a boss adds on top of that.
//   - IgnoreDefense: the share of a monster's PDRate ignored before the
//     attacking skill's own ignoreMobpdpR.
//   - HPSteal: percent of dealt damage healed by an active COMBO_DRAIN buff
//     (Combo Drain, Vampiric Touch, Blood Drain). Bonus.HPOnHit is flat HP that
//     no handler sets or reads, and per-skill drains (Drain, MP Eater) are not
//     a character stat, so neither is shown.
//   - StatusResist, ElementResist and AvoidChance: the effective ASR, TER and
//     dodgeChance. The client rolls them; the server only reports them.
//   - DamageReduce: the share of a monster hit TakenDamage removes, without
//     Satellite Safe, which only absorbs hits above a MaxHP threshold.
//
// No server mechanism exists for PointRate (always 100) or for ReflectDamage,
// ReflectChance, MPSteal, HPStealChance and MPStealChance (always 0).
func StatDetail(c domain.Character, now time.Time) protocol.StatDetail {
	// An unresolved potential source contributes what did resolve; attacks
	// are rejected separately by PlayerStatsChecked.
	pot, effects, _ := PotentialBonusesChecked(c, now)
	buff, _ := BuffBonus(c, now)
	src := statDetailSources{Bonus: pot.Add(Bonuses(c, now)).Add(buff),
		DamagePercents: effects.DamagePercents, BossDamagePercents: effects.BossDamagePercents,
		ExpRate: domain.ExpRate(), MesoRate: domain.MesoRate(), DropRate: domain.DropRate(), DamageCap: int64(combat.MaxLineDamage())}
	if v, ok := ActiveStat(c, "COMBO_DRAIN", now); ok {
		src.HPSteal = v
	}
	// Probe TakenDamage itself so the number cannot drift from what a hit loses.
	const probe = 10000
	steady := c
	steady.SkillBuffs = nil
	for _, row := range c.SkillBuffs {
		if row.Stat != "SATELLITESAFE_ABSORB" {
			steady.SkillBuffs = append(steady.SkillBuffs, row)
		}
	}
	hp, mp := TakenDamage(steady, probe, now)
	src.DamageReduce = (probe - hp - mp) * 100 / probe
	return src.detail()
}

func (s statDetailSources) detail() protocol.StatDetail {
	b := s.Bonus
	// combat's line ceiling: (100 + damR [+ boss damR]) percent, then the
	// potential multiplier of that monster class.
	damage := func(bonus int, lines []int) int64 {
		f := float64(100 + max(bonus, 0))
		for _, v := range lines {
			f *= 1 + float64(v)/100
		}
		return int64(math.Round(f)) - 100
	}
	total := damage(b.DamagePercent, s.DamagePercents)
	return protocol.StatDetail{
		ExpRate:       statInt32(s.ExpRate * int64(100+max(b.EXP, 0))),
		DropRate:      statInt32(s.DropRate * 100),
		MesoRate:      statInt32(s.MesoRate * 100),
		PointRate:     100,
		DamageCap:     s.DamageCap,
		CritDamage:    statInt32(int64(50 + max(b.CriticalDamage, 0))),
		TotalDamage:   statInt32(total),
		BossDamage:    statInt32(damage(b.DamagePercent+b.BossDamagePercent, s.BossDamagePercents) - total),
		IgnoreDefense: statInt32(int64(min(100, max(0, b.IgnoreDefense)))),
		HPSteal:       statInt32(int64(max(s.HPSteal, 0))),
		StatusResist:  statInt32(int64(b.ASR)),
		ElementResist: statInt32(int64(b.TER)),
		DamageReduce:  statInt32(int64(s.DamageReduce)),
		AvoidChance:   statInt32(int64(b.AvoidRate)),
	}
}

func statInt32(v int64) int32 { return int32(min(max(v, math.MinInt32), math.MaxInt32)) }
