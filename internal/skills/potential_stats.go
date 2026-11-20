package skills

import (
	"fmt"
	"math"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

type effectiveStatsProvider struct{}

func init() {
	content.PotentialPhysicalBoundsProvider = func(c domain.Character, now time.Time) (content.PotentialPhysicalMaximum, error) {
		stats, err := PlayerStatsChecked(c, now)
		if err != nil {
			return nil, err
		}
		return func(m content.Mob, skillID, damagePercent int) (int, error) {
			if damagePercent < 1 || damagePercent > 100000 {
				return 0, fmt.Errorf("invalid potential physical damage percentage")
			}
			return combat.LineCeiling(combat.DamageContext{Stats: stats, Mob: m, Effect: combat.Effect{SkillID: skillID, Damage: damagePercent}}), nil
		}, nil
	}
}

func (effectiveStatsProvider) PlayerStats(c domain.Character, now time.Time) combat.PlayerStats {
	return PlayerStats(c, now)
}
func (effectiveStatsProvider) PlayerStatsChecked(c domain.Character, now time.Time) (combat.PlayerStats, error) {
	return PlayerStatsChecked(c, now)
}

// PlayerStats retains the public compatibility shape but returns invalid attack
// stats on an unresolved source. AttackStats uses the checked method and keeps
// the original error; it never falls back to an equipment-only success.
func PlayerStats(c domain.Character, now time.Time) combat.PlayerStats {
	p, err := PlayerStatsChecked(c, now)
	if err != nil {
		p.STR = -1
	}
	return p
}

func floorPercent(base, percent int) int {
	n := int64(base) * int64(percent)
	q := n / 100
	if n%100 < 0 {
		q--
	}
	return int(q)
}

// PotentialBonusesChecked maps additive, already-resolved source fields to the
// existing Bonus vocabulary. This is not evidence that recovery, resistance,
// movement or economy consumers have been implemented. Pending event lines
// stay explicit on the returned report and produce a checked error.
func PotentialBonusesChecked(c domain.Character, now time.Time) (Bonus, content.PotentialEffects, error) {
	effects, err := content.ResolvePotentialEffects(os.Getenv("MAPLEGO_WZ_ROOT"), c, now)
	if err != nil {
		return Bonus{}, effects, err
	}
	v := effects.Values
	b := Bonus{STR: v["incSTR"], DEX: v["incDEX"], INT: v["incINT"], LUK: v["incLUK"],
		STRPercent: v["incSTRr"], DEXPercent: v["incDEXr"], INTPercent: v["incINTr"], LUKPercent: v["incLUKr"],
		MaxHP: v["incMHP"], MaxMP: v["incMMP"], MaxHPPercent: v["incMHPr"], MaxMPPercent: v["incMMPr"],
		WATK: v["incPAD"], MATK: v["incMAD"], WATKPercent: v["incPADr"], MATKPercent: v["incMADr"],
		WDEF: v["incPDD"], MDEF: v["incMDD"], WDEFPercent: v["incPDDr"], MDEFPercent: v["incMDDr"],
		ACC: v["incACC"], ACCPercent: v["incACCr"], AVOID: v["incEVA"], Speed: v["incSpeed"], Jump: v["incJump"],
		AVOIDPercent: v["incEVAr"], Critical: v["incCr"], CriticalDamage: v["incCriticaldamageMax"],
		CriticalDamageMin: v["incCriticaldamageMin"], IgnoreDefense: v["ignoreTargetDEF"], ASR: v["incAsrR"], TER: v["incTerR"]}
	if len(effects.Pending) > 0 {
		return b, effects, &content.PendingPotentialEffectsError{Effects: effects.Pending}
	}
	return b, effects, nil
}

// PlayerStatsChecked inserts equipment potential at the equipment phase. The
// no-potential branch deliberately retains all preexisting scalar behavior.
// Potential four-stat % acts on base+worn+potential-flat; Maple Warrior remains
// base-only and active flat buffs are not multiplied by equipment potential.
func PlayerStatsChecked(c domain.Character, now time.Time) (combat.PlayerStats, error) {
	return playerStatsCheckedForMode(c, now, false)
}

// BattleSquareMeleeStats uses v148 arena physical-defense coefficients.
func BattleSquareMeleeStats(c domain.Character, now time.Time) (combat.PlayerStats, error) {
	return playerStatsCheckedForMode(c, now, true)
}

func playerStatsCheckedForMode(c domain.Character, now time.Time, battleSquare bool) (combat.PlayerStats, error) {
	base, err := combat.BaseStats(c, now)
	if err != nil {
		return base, err
	}
	pot, effects, err := PotentialBonusesChecked(c, now)
	if err != nil {
		return base, err
	}
	if len(effects.Lines) == 0 {
		p := legacyPlayerStatsForMode(c, now, battleSquare)
		attachMonsterPotentialProcs(&p, c, nil)
		p.ResourceCosts, err = ResourceModifiers(c, now)
		return p, err
	}
	p := base
	passive := Bonuses(c, now)
	buff, mw := BuffBonus(c, now)
	stat := func(baseValue, wornValue, flat, pct, passiveFlat, passivePct, buffFlat int) int {
		total := wornValue + flat
		return total + floorPercent(total, pct) + passiveFlat + floorPercent(baseValue, passivePct+mw) + buffFlat
	}
	p.STR = stat(c.STR, p.STR, pot.STR, pot.STRPercent, passive.STR, passive.STRPercent, buff.STR)
	p.DEX = stat(c.DEX, p.DEX, pot.DEX, pot.DEXPercent, passive.DEX, passive.DEXPercent, buff.DEX)
	p.INT = stat(c.INT, p.INT, pot.INT, pot.INTPercent, passive.INT, passive.INTPercent, buff.INT)
	p.LUK = stat(c.LUK, p.LUK, pot.LUK, pot.LUKPercent, passive.LUK, passive.LUKPercent, buff.LUK)
	basicINT := p.INT
	preINT := p.INT - buff.INT - floorPercent(c.INT, mw)
	// Java PlayerStats1500..1502 applies the equipment magic percentage to
	// local INT too. Do not multiply subsequent flat INT/MW by this source.
	p.INT += floorPercent(preINT, pot.MATKPercent+infinityMagicPercent(c, now))
	p.WATK += pot.WATK + passive.WATK
	p.WATK += floorPercent(p.WATK, pot.WATKPercent+passive.WATKPercent+buff.WATKPercent)
	p.WATK += buff.WATK
	p.MATK += pot.MATK + passive.MATK
	p.MATK += floorPercent(p.MATK, pot.MATKPercent+passive.MATKPercent+buff.MATKPercent)
	p.MATK += buff.MATK
	foldAccuracy(&p, c.Traits.Insight, pot.ACC+passive.ACC+buff.ACC, pot.ACCPercent+passive.ACCPercent+buff.ACCPercent)
	foldDefenseMovement(&p, c, basicINT, mw, pot, passive, buff, battleSquare)
	// 0x7069EE adds source percentages before scaling the complete EVA total.
	foldAvoidability(&p, c, pot.AVOID+passive.AVOID+buff.AVOID, pot.AVOIDPercent+passive.AVOIDPercent+buff.AVOIDPercent)
	p.Avoid += floorPercent(p.Avoid, passive.GuildAVOIDPercent)
	p.PotentialAvoidPercent = pot.AVOIDPercent
	p.DodgeRate = passive.AvoidRate + buff.AvoidRate
	p.ASR = pot.ASR + passive.ASR + buff.ASR
	p.TER = pot.TER + passive.TER + buff.TER
	p.CritDamageMin = pot.CriticalDamageMin + passive.CriticalDamageMin + buff.CriticalDamageMin
	p.Mastery += passive.Mastery + buff.Mastery
	p.CritRate += pot.Critical + passive.Critical + buff.Critical
	p.BossCritRate += passive.BossCritical + buff.BossCritical
	p.StunnedCritRate += passive.StunnedCritical + buff.StunnedCritical
	p.CritDamage += pot.CriticalDamage + passive.CriticalDamage + buff.CriticalDamage
	p.DamageRate += passive.DamagePercent + buff.DamagePercent
	p.BossDamageRate += passive.BossDamagePercent + buff.BossDamagePercent
	p.ComboDamagePercent = ComboDamagePercent(c, now)
	p.IgnoreDefense += pot.IgnoreDefense + passive.IgnoreDefense + buff.IgnoreDefense
	p.FullDefenseIgnore = passive.FullDefenseIgnore
	p.ElementAmp += passive.ElementAmp + buff.ElementAmp
	p.ShadowPartner = shadowPartner(c, now)
	p.FinalCutPercent, _ = ActiveStat(c, "FINAL_CUT", now)
	p.BerserkFuryPercent, _ = ActiveStat(c, "BERSERK_FURY", now)
	p.ChargeDamagePercent = chargeDamagePercent(c, now)
	p.SkillDamage = passive.Add(buff).SkillDamagePercents()
	factor := func(values []int) (float64, error) {
		if len(values) == 0 {
			return 0, nil
		}
		f := 1.0
		for _, v := range values {
			if v < 0 {
				return 0, fmt.Errorf("unsupported negative potential damage multiplier")
			}
			f *= 1 + float64(v)/100
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("potential multiplier overflow")
		}
		return f, nil
	}
	if p.PotentialDamageMultiplier, err = factor(effects.DamagePercents); err != nil {
		return p, err
	}
	if p.PotentialBossDamageMultiplier, err = factor(effects.BossDamagePercents); err != nil {
		return p, err
	}
	// Every attack path later multiplies the base by at most100000. Reject
	// a source that would overflow that existing integer pipeline, not silently
	// wrap into a plausible damage ceiling.
	for _, v := range []int{p.STR, p.DEX, p.INT, p.LUK, p.WATK, p.MATK, p.Accuracy} {
		if v < 0 || int64(v) > math.MaxInt32 {
			return p, fmt.Errorf("invalid potential effective stats")
		}
	}
	main := int64(max(p.STR, p.DEX, p.INT, p.LUK))
	atk := int64(max(p.WATK, p.MATK, 1))
	if main > 0 && (main > math.MaxInt64/1200/atk || main*1200*atk > math.MaxInt64/100000) {
		return p, fmt.Errorf("potential attack arithmetic overflow")
	}
	attachMonsterPotentialProcs(&p, c, effects.AttackProcs)
	p.ResourceCosts, err = ResourceModifiers(c, now)
	return p, err
}
