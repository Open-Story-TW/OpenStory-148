package skills

import (
	"os"
	"sort"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// Stats is the S0a effective-stat provider installed with
// combat.SetStatsProvider: equipment (combat.BaseStats), then registered
// passives and active buffs, following Java PlayerStats.
var Stats combat.StatsProvider = effectiveStatsProvider{}

// MovementPassive reads the permanent v148 movement component of learned skills.
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
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	total := guildBonuses(root, c, now).Add(characterCardBonuses(root, c.Cards).Add(innerAbilityBonuses(root, c)))
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
		bonus.SpeedLimitBonus = max(bonus.SpeedLimitBonus, values["speedMax"])
		if !def.CanLearnBy(c) {
			bonus.Mastery = 0
		}
		total = total.Add(bonus)
	}
	dice := content.ActiveDiceBonuses(c, now)
	total.WDEFPercent += dice.DefensePercent
	total.Critical += dice.Critical
	total.DamagePercent += dice.DamagePercent
	total.EXP += dice.ExperiencePercent
	return total
}

// Add combines two bonuses. The highest speed limit bonus applies.
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
	b.GuildAVOIDPercent += o.GuildAVOIDPercent
	b.GuildWDEFPercent += o.GuildWDEFPercent
	b.GuildMDEFPercent += o.GuildMDEFPercent
	b.AvoidRate += o.AvoidRate
	b.Critical += o.Critical
	b.BossCritical += o.BossCritical
	b.CriticalDamage += o.CriticalDamage
	b.CriticalDamageMin += o.CriticalDamageMin
	b.StunnedCritical += o.StunnedCritical
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
	b.BuffDurationPercent += o.BuffDurationPercent
	return b
}

// BuffBonus turns the active buff stats Java PlayerStats reads into a Bonus.
// Maple Warrior is returned separately because it scales base stats only.
func BuffBonus(c domain.Character, now time.Time) (b Bonus, mapleWarrior int) {
	// The client's own PAD/ACC/EVA buff stats; a full Energy Charge competes
	// with them instead of adding (see energyChargeValues).
	var pad, acc, avoid int
	for _, row := range c.SkillBuffs {
		if !row.ExpiresAt.After(now) {
			continue
		}
		v := row.Value
		switch row.Stat {
		case "WATK":
			pad += v
		case "ACC":
			acc += v
		case "AVOID":
			avoid += v
		case "ENHANCED_WATK", "ANGEL_ATK":
			b.WATK += v
		case "MATK", "ENHANCED_MATK", "ANGEL_MATK":
			b.MATK += v
		case "WDEF", "ENHANCED_WDEF":
			b.WDEF += v
		case "MDEF", "ENHANCED_MDEF":
			b.MDEF += v
		case "DEFENCE_BOOST_R":
			// Original v148 705E41/706330 both add getter6EEC2D (bit186)
			// to the defense rate. Original Soul Endure panels double both.
			b.WDEFPercent += v
			b.MDEFPercent += v
		case "ANGEL_ACC":
			b.ACC += v
		case "ANGEL_AVOID":
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
		case "HAMSTRING":
			if row.ID == 3121007 {
				b.AvoidRate += v
			}
		case "BLIND":
			if row.ID == 3221006 {
				b.AvoidRate += v
			}
		case "ELEMENTAL_STATUS_R":
			b.TER += v
		case "INDIE_DAMAGE":
			b.DamagePercent += v
		case "HOLY_SYMBOL":
			b.EXP += v
		case "MESOUP":
			if v > 100 {
				b.MesoRate += v - 100
			} else if v > 0 {
				b.MesoRate += v
			}
		case "BLESS":
			if row.ID != 2301004 {
				continue
			}
			if def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), row.ID); err == nil {
				if values, err := def.Values(min(max(v, 1), def.MaxLevel)); err == nil {
					b.WATK += values["x"]
					b.MATK += values["y"]
					b.WDEF += values["z"]
					b.MDEF += values["u"]
					b.ACC += values["v"]
					b.AVOID += values["w"]
				}
			}
		case "YELLOW_AURA":
			skillID := row.ID
			if skillID == 32101003 && EffectiveLevel(c, 32120001, now) > 0 {
				skillID = 32120001
			}
			if def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), skillID); err == nil {
				if values, err := def.Values(min(max(v, 1), def.MaxLevel)); err == nil {
					b.Speed += values["x"]
				}
			}
		case "BLUE_AURA":
			// v148 uses z of the active advanced source and its actual rank.
			if row.ID != AdvancedBlueAuraSkill && row.ID != 32110008 {
				continue
			}
			if def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), row.ID); err == nil {
				if values, err := def.Values(min(max(v, 1), def.MaxLevel)); err == nil {
					percent := values["z"]
					if row.ID == 32110008 { // Preserve Body Boost's separate effect.
						percent += values["y"]
					}
					b.WDEFPercent += percent
					b.MDEFPercent += percent
				}
			}
		}
	}
	energy := energyChargeValues(c, now)
	b.WATK += max(pad, energy["pad"])
	b.ACC += max(acc, energy["acc"])
	b.AVOID += max(avoid, energy["eva"])
	return b, mapleWarrior
}

// energyChargeValues are the WZ values of the job's Energy Charge while its
// gauge is full, nil otherwise. v148 0x6EF837 (PAD, summed by the attack
// stat builder 0x7057B1), 0x6EF8E9 (ACC) and 0x6EF99B (EVA) return
// max(buff stat, Energy Charge level data) while the gauge is full (two-state
// value >= 10000, as 0x6F1954 reads it). The skill comes from the job, as in
// those getters: 15100004 for Cygnus, 5810001 for jobs 509/58x/59x,
// otherwise 5110001.
func energyChargeValues(c domain.Character, now time.Time) map[string]int {
	full := false
	for _, row := range c.SkillBuffs {
		if row.Stat == "ENERGY_CHARGE" && row.Value >= 10_000 && row.ExpiresAt.After(now) {
			full = true
		}
	}
	if !full {
		return nil
	}
	id := 5110001
	switch {
	case c.Job/1000 == 1:
		id = 15100004
	case c.Job/10 == 58 || c.Job/10 == 59 || c.Job == 509:
		id = 5810001
	}
	return learnedValues(c, id, now)
}

// PlayerStats is the effective attacker view used by the S0b pipeline.
func legacyPlayerStats(c domain.Character, now time.Time) combat.PlayerStats {
	return legacyPlayerStatsForMode(c, now, false)
}

func legacyPlayerStatsForMode(c domain.Character, now time.Time, battleSquare bool) combat.PlayerStats {
	p, err := combat.BaseStats(c, now)
	if err != nil {
		return p
	}
	passive := Bonuses(c, now)
	buff, mw := BuffBonus(c, now)
	b := passive.Add(buff)
	scale := func(base, total, percent int) int { return total + int(int64(base)*int64(percent)/100) }
	str, dex, intl, luk := c.STR, c.DEX, c.INT, c.LUK
	p.STR = scale(str, p.STR+b.STR, b.STRPercent+mw)
	p.DEX = scale(dex, p.DEX+b.DEX, b.DEXPercent+mw)
	p.INT = scale(intl, p.INT+b.INT, b.INTPercent+mw)
	basicINT := p.INT
	// Java percent_matk also scales pre-MW INT; late flat INT is excluded.
	preINT := p.INT - buff.INT - floorPercent(c.INT, mw)
	p.INT += floorPercent(preINT, infinityMagicPercent(c, now))
	p.LUK = scale(luk, p.LUK+b.LUK, b.LUKPercent+mw)
	foldDefenseMovement(&p, c, basicINT, mw, Bonus{}, passive, buff, battleSquare)
	p.WATK += b.WATK
	p.WATK += p.WATK * b.WATKPercent / 100
	p.MATK += b.MATK
	p.MATK += p.MATK * b.MATKPercent / 100
	foldAccuracy(&p, c.Traits.Insight, b.ACC, b.ACCPercent)
	foldAvoidability(&p, c, b.AVOID, b.AVOIDPercent)
	p.Avoid += floorPercent(p.Avoid, b.GuildAVOIDPercent)
	p.Mastery += b.Mastery
	p.CritRate += b.Critical
	p.BossCritRate += b.BossCritical
	p.StunnedCritRate += b.StunnedCritical
	p.CritDamage += b.CriticalDamage
	p.CritDamageMin += b.CriticalDamageMin
	p.ASR += b.ASR
	p.TER += b.TER
	p.DamageRate += b.DamagePercent
	p.BossDamageRate += b.BossDamagePercent
	p.ComboDamagePercent = ComboDamagePercent(c, now)
	p.IgnoreDefense += b.IgnoreDefense
	p.FullDefenseIgnore = b.FullDefenseIgnore
	p.ElementAmp += b.ElementAmp
	p.ShadowPartner = shadowPartner(c, now)
	p.FinalCutPercent, _ = ActiveStat(c, "FINAL_CUT", now)
	p.BerserkFuryPercent, _ = ActiveStat(c, "BERSERK_FURY", now)
	p.ChargeDamagePercent = chargeDamagePercent(c, now)
	p.SkillDamage = b.SkillDamagePercents()
	// The potential fold reports dodge too; without it a learned passive's
	// dodge showed as zero for characters without potential lines.
	p.DodgeRate = b.AvoidRate
	return p
}

// foldDefenseMovement uses the same defense and movement arithmetic for
// equipment with or without potential. These stats are independently
// observable in the authorized QA report.
func foldDefenseMovement(p *combat.PlayerStats, c domain.Character, basicINT, mapleWarrior int, pot, passive, buff Bonus, battleSquare bool) {
	for _, e := range c.Equipment {
		if e.Slot >= 0 || e.Slot <= -100 {
			continue
		}
		p.WDEF += e.WDEF
		p.MDEF += e.MDEF
		p.Speed += e.Speed
		p.Jump += e.Jump
	}
	p.WDEF += pot.WDEF + passive.WDEF
	p.MDEF += pot.MDEF + passive.MDEF
	str, dex, luk := p.STR, p.DEX, p.LUK
	if battleSquare {
		// Guardian Gloves retain the arena's pre-buff physical stat inputs.
		str -= buff.STR + floorPercent(c.STR, mapleWarrior)
		dex -= buff.DEX + floorPercent(c.DEX, mapleWarrior)
		luk -= buff.LUK + floorPercent(c.LUK, mapleWarrior)
	}
	// v148 BasicStat helpers 0x6F1475 and 0x6F14E0 truncate the combined
	// weighted primary stats once, with Will included before defense %.
	will := 5 * c.Traits.WillTier()
	p.WDEF += (15*str+4*(dex+luk))/10 + will
	p.MDEF += (15*basicINT+4*(p.DEX+p.LUK))/10 + will
	physical, magic := p.WDEF, p.MDEF
	physicalPercent := pot.WDEFPercent + passive.WDEFPercent + buff.WDEFPercent
	magicPercent := pot.MDEFPercent + passive.MDEFPercent + buff.MDEFPercent
	// v148 calculates each defense percentage from its own pre-percent total.
	// CUIStatDetail uses the same separate physical and magic bases for guild ranks.
	p.WDEF = physical + min(30000, floorPercent(physical, physicalPercent+passive.GuildWDEFPercent))
	p.MDEF = magic + min(30000, floorPercent(magic, magicPercent))
	p.MDEF += floorPercent(magic, passive.GuildMDEFPercent)
	p.WDEF += buff.WDEF
	p.MDEF += buff.MDEF
	p.Speed += 100 + pot.Speed + passive.Speed + buff.Speed
	p.Jump += 100 + pot.Jump + passive.Jump + buff.Jump
	speedLimit := 140 + passive.SpeedLimitBonus
	p.Speed = min(p.Speed, speedLimit)
	p.Jump = min(p.Jump, 123)
}

// CUIStatDetail's basic EVA helpers (0x6F1629/0x6F1698) add Insight's
// five-level bonus. These are the cumulative EXP thresholds from 0x6EB087.
var insightStatEXP = [...]int{
	0, 181, 851, 2273, 4563, 7562, 11040, 14893, 19044, 23515, 28331,
	33519, 39110, 45135, 51626, 58371, 65216, 72161, 79206, 86351, 93596,
}

func insightHitStat(experience int) int {
	tier := sort.Search(len(insightStatEXP), func(i int) bool { return insightStatEXP[i] > experience }) - 1
	if tier <= 0 {
		return 0
	}
	return 10 + 5*tier*(tier-1)/2
}

func hitStatTotal(base, percent int) int {
	if percent > 0 {
		base += floorPercent(base, percent)
	}
	return min(9999, max(0, base))
}

// 0x6F154B and 0x6F15BA derive the two basic accuracy values. 0x70681C
// applies the combined percentage after equipment and learned flat sources.
func foldAccuracy(p *combat.PlayerStats, insightEXP, flat, percent int) {
	bonus := insightHitStat(insightEXP)
	flat += p.Accuracy
	accuracy := func(light, medium int) int {
		base := float64(p.LUK)*0.8 + float64(medium)*1.6 + float64(light)*0.4 + float64(bonus)
		return hitStatTotal(int(base)+flat, percent)
	}
	p.Accuracy = accuracy(p.STR, p.DEX)
	p.MagicAccuracy = accuracy(p.DEX, p.INT)
}

func foldAvoidability(p *combat.PlayerStats, c domain.Character, flat, percent int) {
	for _, e := range c.Equipment {
		if e.Slot < 0 && e.Slot > -100 {
			flat += e.AVOID
		}
	}
	insight := insightHitStat(c.Traits.Insight)
	avoid := func(light, medium int) int {
		// Preserve the native coefficient order before truncating basic EVA.
		value := float64(p.LUK)*1.4 + float64(medium)*0.6 + float64(light)*0.2 + float64(insight)
		return hitStatTotal(int(value)+flat, percent)
	}
	p.Avoid = avoid(p.STR, p.DEX)
	p.MagicAvoid = avoid(p.DEX, p.INT)
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

// chargeDamagePercent returns the active WK_CHARGE WZ damage multiplier
// applied by v148 CalcDamage::PDamage (0x71289E -> 0x6EF433 -> 0x5F6B6D).
func chargeDamagePercent(c domain.Character, now time.Time) int {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	for _, row := range c.SkillBuffs {
		if row.Stat != "WK_CHARGE" || !row.ExpiresAt.After(now) {
			continue
		}
		switch row.ID {
		case 1211004, 1211006, 1211008, 1221004, 11111007, 15101006, 21101006:
		default:
			continue
		}
		def, err := content.SkillDefinition(root, row.ID)
		if err != nil {
			continue
		}
		lvl := row.Level
		if lvl < 1 {
			lvl = EffectiveLevel(c, row.ID, now)
		}
		if lvl < 1 {
			continue
		}
		values, err := def.Values(min(lvl, def.MaxLevel))
		if err == nil && values["damage"] > 100 {
			return values["damage"]
		}
	}
	return 0
}

func init() { content.VitalsPassives = vitalsPassives }

// vitalsPassives feeds registered MaxHP/MaxMP passives into
// content.DerivedVitals (Java PlayerStats percent_hp/percent_mp).
func vitalsPassives(c domain.Character, now time.Time) (hpFlat, mpFlat, hpPercent, mpPercent int, ids map[int]bool) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	external := characterCardBonuses(root, c.Cards).Add(innerAbilityBonuses(root, c))
	hpFlat, mpFlat, hpPercent, mpPercent = external.MaxHP, external.MaxMP, external.MaxHPPercent, external.MaxMPPercent
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
