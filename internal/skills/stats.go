package skills

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"os"
	"time"
)

// Stats is the S0a effective-stat provider installed with
// combat.SetStatsProvider: equipment (combat.BaseStats), then registered
// passives and active buffs, following Java PlayerStats.
var Stats combat.StatsProvider = effectiveStatsProvider{}

// MovementPassive reads the learned speed, jump and speed cap from v148 WZ.
func MovementPassive(c *PassiveContext) Bonus {
	return Bonus{Speed: c.Values["psdSpeed"], Jump: c.Values["psdJump"], SpeedLimitBonus: c.Values["speedMax"]}
}

// BasicMastery returns the prerequisite's mastery when no eligible advanced
// value replaces it. Other bonuses from the prerequisite still apply.
func (c *PassiveContext) BasicMastery(advancedID int) int {
	base := c.Values["mastery"]
	level := EffectiveLevel(c.Character, advancedID, c.Now)
	if base == 0 || level < 1 {
		return base
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), advancedID)
	if err != nil || def.Disabled || !def.CanLearnBy(c.Character) {
		return base
	}
	if values, err := def.Values(level); err == nil && values["mastery"] != 0 {
		return 0
	}
	return base
}

// Bonuses sums the Passive of every learned, unexpired registered skill. The
// client looks mastery up by job and weapon, so another line's mastery must not
// raise the damage floor; its other passives, critical rate included, it does count.
func Bonuses(c domain.Character, now time.Time) Bonus {
	var total Bonus
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	for _, s := range c.Skills {
		if s.Level < 1 || !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			continue
		}
		h, ok := Lookup(s.ID)
		if !ok || h.Passive == nil {
			continue
		}
		def, err := content.SkillDefinition(root, s.ID)
		if err != nil || def.Disabled {
			continue
		}
		level := EffectiveLevel(c, s.ID, now)
		values, err := def.Values(level)
		if err != nil {
			continue
		}
		bonus := h.Passive(&PassiveContext{Skill: def, Level: level, Values: values, Character: c, Now: now})
		if !def.CanLearnBy(c) {
			bonus.Mastery = 0
		}
		total = total.Add(bonus)
	}
	return total
}

// Add combines two bonuses, using the highest speed limit bonus.
func (b Bonus) Add(o Bonus) Bonus {
	b.MaxHP += o.MaxHP
	b.MaxMP += o.MaxMP
	b.MaxHPPercent += o.MaxHPPercent
	b.MaxMPPercent += o.MaxMPPercent
	b.STR += o.STR
	b.DEX += o.DEX
	b.INT += o.INT
	b.LUK += o.LUK
	b.STRPercent += o.STRPercent
	b.DEXPercent += o.DEXPercent
	b.INTPercent += o.INTPercent
	b.LUKPercent += o.LUKPercent
	b.WATK += o.WATK
	b.MATK += o.MATK
	b.WDEF += o.WDEF
	b.MDEF += o.MDEF
	b.WATKPercent += o.WATKPercent
	b.MATKPercent += o.MATKPercent
	b.WDEFPercent += o.WDEFPercent
	b.MDEFPercent += o.MDEFPercent
	b.ACC += o.ACC
	b.AVOID += o.AVOID
	b.Speed += o.Speed
	b.Jump += o.Jump
	b.SpeedLimitBonus = max(b.SpeedLimitBonus, o.SpeedLimitBonus)
	b.ACCPercent += o.ACCPercent
	b.AVOIDPercent += o.AVOIDPercent
	b.AvoidRate += o.AvoidRate
	b.Critical += o.Critical
	b.BossCritical += o.BossCritical
	b.CriticalDamage += o.CriticalDamage
	b.CriticalDamageMin += o.CriticalDamageMin
	b.Mastery += o.Mastery
	b.DamagePercent += o.DamagePercent
	b.BossDamagePercent += o.BossDamagePercent
	b.IgnoreDefense += o.IgnoreDefense
	b.FullDefenseIgnore = b.FullDefenseIgnore || o.FullDefenseIgnore
	for _, d := range o.SkillDamage {
		if d.Skill == 0 {
			continue
		}
		for i := range b.SkillDamage {
			if b.SkillDamage[i].Skill == d.Skill || b.SkillDamage[i].Skill == 0 {
				b.SkillDamage[i].Skill = d.Skill
				b.SkillDamage[i].Percent += d.Percent
				break
			}
		}
	}
	b.ElementAmp += o.ElementAmp
	b.MPConsume += o.MPConsume
	b.DOTPercent += o.DOTPercent
	b.DOTTime += o.DOTTime
	b.TER += o.TER
	b.ASR += o.ASR
	b.HPRecovery += o.HPRecovery
	b.MPRecovery += o.MPRecovery
	b.HPRecoveryRate += o.HPRecoveryRate
	b.MPRecoveryRate += o.MPRecoveryRate
	b.HPOnHit += o.HPOnHit
	b.PotionPercent += o.PotionPercent
	b.MesoRate += o.MesoRate
	b.Range += o.Range
	b.EXP += o.EXP
	b.DamageReduce += o.DamageReduce
	return b
}

// BuffBonus turns the active buff stats Java PlayerStats reads into a Bonus.
// Maple Warrior is returned separately because it scales base stats only.
func BuffBonus(c domain.Character, now time.Time) (b Bonus, mapleWarrior int) {
	for _, row := range c.SkillBuffs {
		if !row.ExpiresAt.After(now) {
			continue
		}
		v := row.Value
		switch row.Stat {
		case "WATK", "ENHANCED_WATK", "ANGEL_ATK":
			b.WATK += v
		case "MATK", "ENHANCED_MATK", "ANGEL_MATK":
			b.MATK += v
		case "WDEF", "ENHANCED_WDEF":
			b.WDEF += v
		case "MDEF", "ENHANCED_MDEF":
			b.MDEF += v
		case "ACC", "ANGEL_ACC":
			b.ACC += v
		case "AVOID", "ANGEL_AVOID":
			b.AVOID += v
		case "SPEED", "ANGEL_SPEED":
			b.Speed += v
		case "JUMP", "ANGEL_JUMP":
			b.Jump += v
		case "STR":
			b.STR += v
		case "DEX":
			b.DEX += v
		case "INT":
			b.INT += v
		case "LUK":
			b.LUK += v
		case "ANGEL_STAT":
			b.STR += v
			b.DEX += v
			b.INT += v
			b.LUK += v
		case "INFINITY":
			// Value starts at 1 and grows by WZ damage on each committed tick.
			b.MATKPercent += max(0, v-1)
		case "MAPLE_WARRIOR":
			mapleWarrior += v
		case "SHARP_EYES":
			// Java packs (x << 8) + criticaldamageMax.
			b.Critical += v >> 8
			b.CriticalDamage += v & 0xFF
		case "ECHO_OF_HERO":
			b.WATKPercent += v
			b.MATKPercent += v
		case "ABNORMAL_STATUS_R", "MAGIC_RESISTANCE":
			b.ASR += v
		case "ONYX_SHROUD":
			b.AvoidRate += v
		case "ELEMENTAL_STATUS_R":
			b.TER += v
		case "BLUE_AURA":
			// The value is the source skill level; Java adds that level's
			// y+z to the defense percentages.
			if def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), row.ID); err == nil {
				if values, err := def.Values(min(max(v, 1), def.MaxLevel)); err == nil {
					b.WDEFPercent += values["y"] + values["z"]
					b.MDEFPercent += values["y"] + values["z"]
				}
			}
		}
	}
	return b, mapleWarrior
}

// PlayerStats is the effective attacker view used by the S0b pipeline.
func legacyPlayerStats(c domain.Character, now time.Time) combat.PlayerStats {
	p, err := combat.BaseStats(c, now)
	if err != nil {
		return p
	}
	passive := Bonuses(c, now)
	buff, mw := BuffBonus(c, now)
	b := passive.Add(buff)
	foldMovement(&p, c, Bonus{}, passive, buff)
	scale := func(base, total, percent int) int { return total + int(int64(base)*int64(percent)/100) }
	str, dex, intl, luk := c.STR, c.DEX, c.INT, c.LUK
	p.STR = scale(str, p.STR+b.STR, b.STRPercent+mw)
	p.DEX = scale(dex, p.DEX+b.DEX, b.DEXPercent+mw)
	p.INT = scale(intl, p.INT+b.INT, b.INTPercent+mw)
	basicINT := p.INT
	// Java percent_matk also scales pre-MW INT; late flat INT is excluded.
	p.INT += floorPercent(p.INT-buff.INT-floorPercent(c.INT, mw), infinityMagicPercent(c, now))
	p.LUK = scale(luk, p.LUK+b.LUK, b.LUKPercent+mw)
	foldDefense(&p, c, basicINT, Bonus{}, passive, buff)
	p.WATK += b.WATK
	p.WATK += p.WATK * b.WATKPercent / 100
	p.MATK += b.MATK
	p.MATK += p.MATK * b.MATKPercent / 100
	p.Accuracy += b.ACC
	p.Accuracy += p.Accuracy * b.ACCPercent / 100
	p.Mastery += b.Mastery
	p.CritRate += b.Critical
	p.BossCritRate += b.BossCritical
	p.CritDamage += b.CriticalDamage
	p.DamageRate += b.DamagePercent
	p.BossDamageRate += b.BossDamagePercent
	p.IgnoreDefense += b.IgnoreDefense
	p.FullDefenseIgnore = b.FullDefenseIgnore
	p.ElementAmp += b.ElementAmp
	p.ShadowPartner = shadowPartner(c, now)
	return p
}

func foldDefense(p *combat.PlayerStats, c domain.Character, basicINT int, pot, passive, buff Bonus) {
	for _, e := range c.Equipment {
		if e.Slot >= 0 || e.Slot <= -100 {
			continue
		}
		p.WDEF += e.WDEF
		p.MDEF += e.MDEF
	}
	p.WDEF += pot.WDEF + passive.WDEF
	p.MDEF += pot.MDEF + passive.MDEF
	// v148 BasicStat helpers 0x6F1475 and 0x6F14E0 truncate the combined
	// weighted primary stats once, with Will included before defense %.
	will := 5 * c.Traits.WillTier()
	p.WDEF += (15*p.STR+4*(p.DEX+p.LUK))/10 + will
	p.MDEF += (15*basicINT+4*(p.DEX+p.LUK))/10 + will
	physical, magic := p.WDEF, p.MDEF
	physicalPercent := pot.WDEFPercent + passive.WDEFPercent + buff.WDEFPercent
	magicPercent := pot.MDEFPercent + passive.MDEFPercent + buff.MDEFPercent
	// v148 calculates each defense percentage from its own pre-percent total.
	p.WDEF = physical + min(30000, floorPercent(physical, physicalPercent))
	p.MDEF = magic + min(30000, floorPercent(magic, magicPercent))
	p.WDEF += buff.WDEF
	p.MDEF += buff.MDEF
}

func foldMovement(p *combat.PlayerStats, c domain.Character, potential, passive, buff Bonus) {
	p.Speed += 100 + potential.Speed + passive.Speed + buff.Speed
	p.Jump += 100 + potential.Jump + passive.Jump + buff.Jump
	for _, e := range c.Equipment {
		if e.Slot < 0 && e.Slot > -100 {
			p.Speed += e.Speed
			p.Jump += e.Jump
		}
	}
	p.Speed = min(p.Speed, 140+passive.SpeedLimitBonus)
	p.Jump = min(p.Jump, 123)
}

// shadowPartner is the extra line percent of an active Shadow Partner: Java
// uses the source skill's WZ x, not the buff value (4211008 sends its level).
func shadowPartner(c domain.Character, now time.Time) int {
	for _, row := range c.SkillBuffs {
		if row.Stat != "SHADOWPARTNER" || !row.ExpiresAt.After(now) {
			continue
		}
		def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), row.ID)
		if err != nil {
			return 0
		}
		values, err := def.Values(min(max(row.Level, 1), def.MaxLevel))
		if err != nil {
			return 0
		}
		return max(0, values["x"])
	}
	return 0
}

func init() { content.VitalsPassives = vitalsPassives }

// vitalsPassives feeds registered MaxHP/MaxMP passives into
// content.DerivedVitals (Java PlayerStats percent_hp/percent_mp).
func vitalsPassives(c domain.Character, now time.Time) (hpFlat, mpFlat, hpPercent, mpPercent int, ids map[int]bool) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	ids = map[int]bool{}
	for _, s := range c.Skills {
		if s.Level < 1 || !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			continue
		}
		h, ok := Lookup(s.ID)
		if !ok || h.Passive == nil {
			continue
		}
		def, err := content.SkillDefinition(root, s.ID)
		if err != nil || def.Disabled {
			continue
		}
		level := EffectiveLevel(c, s.ID, now)
		values, err := def.Values(level)
		if err != nil {
			continue
		}
		b := h.Passive(&PassiveContext{Skill: def, Level: level, Values: values, Character: c, Now: now})
		if b.MaxHP == 0 && b.MaxMP == 0 && b.MaxHPPercent == 0 && b.MaxMPPercent == 0 {
			continue
		}
		hpFlat, mpFlat = hpFlat+b.MaxHP, mpFlat+b.MaxMP
		hpPercent, mpPercent = hpPercent+b.MaxHPPercent, mpPercent+b.MaxMPPercent
		ids[s.ID] = true
	}
	return
}
