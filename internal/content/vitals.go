package content

import (
	"fmt"
	"maple-go-server/internal/domain"
	"os"
	"time"
)

// VitalsPassives is installed by the skill registry: the MaxHP/MaxMP flat and
// percent bonuses of the learned registered passives, and the skill IDs that
// provided them (those skip the built-in policies below).
var VitalsPassives func(c domain.Character, now time.Time) (hpFlat, mpFlat, hpPercent, mpPercent int, ids map[int]bool)

// DerivedVitals is the single authority for effective maxima. Base values are
// never inferred again at runtime or modified by equipment movement.
func DerivedVitals(c domain.Character, now time.Time) (hp, mp int, err error) {
	if c.VitalsModel != 1 || c.BaseMaxHP < 1 || c.BaseMaxHP > domain.VitalCeiling || c.BaseMaxMP < 0 || c.BaseMaxMP > domain.VitalCeiling {
		return 0, 0, fmt.Errorf("explicit or compatibility base vitals required")
	}
	potential, err := ResolvePotentialEffects(os.Getenv("MAPLEGO_WZ_ROOT"), c, now)
	if err != nil {
		return 0, 0, err
	}
	for _, pending := range potential.Pending {
		for _, attribute := range pending.Attributes {
			// All-skill levels can change HP/MP passives indirectly. Until
			// effective skill-level integration exists, those maxima cannot
			// be declared correct from only the direct flat/percent fields.
			if attribute == "incAllskill" {
				return 0, 0, &PendingPotentialEffectsError{Effects: potential.Pending}
			}
		}
	}
	hp, mp = c.BaseMaxHP, c.BaseMaxMP
	// The v148 BasicStat maximum adds Will (0x70F8C6, table 0xC10CB0)
	// before applying percentage bonuses.
	hp += 100 * c.Traits.WillTier()
	hpPercent, mpPercent := 0, 0
	for _, e := range c.Equipment {
		if e.Slot < 0 && e.Slot >= -199 {
			hp += e.HP
			mp += e.MP
		}
	}
	hp += potential.Values["incMHP"]
	mp += potential.Values["incMMP"]
	if hp < 1 || mp < 0 {
		return 0, 0, fmt.Errorf("equipped vitals are below valid limits")
	}
	var registered map[int]bool
	if VitalsPassives != nil {
		var hpFlat, mpFlat int
		hpFlat, mpFlat, hpPercent, mpPercent, registered = VitalsPassives(c, now)
		if hpPercent < 0 || mpPercent < 0 || hpPercent > 1000 || mpPercent > 1000 {
			return 0, 0, fmt.Errorf("unsupported passive maximum percentage")
		}
		hp, mp = max(1, hp+hpFlat), max(0, mp+mpFlat)
	}
	hpPercent += potential.Values["incMHPr"]
	mpPercent += potential.Values["incMMPr"]
	// First-warrior HP Increase (1000006), confirmed WZ mhpR=2*x and
	// Java PlayerStats1288/1671. Other HP/MP passives need their own policy.
	if c.Job >= 100 && c.Job <= 132 {
		for _, s := range c.Skills {
			if s.ID == 1000006 && !registered[s.ID] && s.Level > 0 && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
				definition, e := SkillDefinition("", s.ID)
				if e != nil || s.Level > definition.MaxLevel {
					return 0, 0, fmt.Errorf("invalid learned HP Increase")
				}
				v, e := definition.Values(s.Level)
				if e != nil || v["mhpR"] < 0 || v["mhpR"] > 100 {
					return 0, 0, fmt.Errorf("unsupported HP Increase formula")
				}
				hpPercent += v["mhpR"]
			}
		}
	}
	// Adventurer magician MP Expansion, Skill.wz/200.img/2000006:
	// mmpR=2*x. Java PlayerStats' exact mage ancestry switch adds this
	// percentage after equipment MP; changing the maximum never restores MP.
	switch c.Job {
	case 200, 210, 211, 212, 220, 221, 222, 230, 231, 232:
		for _, state := range c.Skills {
			if state.ID != 2000006 || registered[state.ID] || state.Level < 1 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
				continue
			}
			definition, e := SkillDefinition("", state.ID)
			if e != nil || state.Level > definition.MaxLevel {
				return 0, 0, fmt.Errorf("invalid learned MP Expansion")
			}
			values, e := definition.Values(state.Level)
			if e != nil || values["mmpR"] < 0 || values["mmpR"] > 100 {
				return 0, 0, fmt.Errorf("unsupported MP Expansion formula")
			}
			mpPercent += values["mmpR"]
		}
	}
	// Java PlayerStats adds MAXHP/MAXMP temporary stats (percent) to the
	// passive percentages, then floors once against base plus worn maxima.
	// CONVERSION replaces MAXHP when both are active.
	conversion := false
	for _, buff := range c.SkillBuffs {
		conversion = conversion || buff.Stat == "CONVERSION" && buff.ExpiresAt.After(now)
	}
	for _, buff := range c.SkillBuffs {
		if buff.Stat != "MAXHP" && buff.Stat != "MAXMP" && buff.Stat != "CONVERSION" || !buff.ExpiresAt.After(now) || conversion && buff.Stat == "MAXHP" {
			continue
		}
		if buff.Value < 0 || buff.Value > 1000 {
			return 0, 0, fmt.Errorf("unsupported maximum percentage buff")
		}
		if buff.Stat == "MAXMP" {
			mpPercent += buff.Value
		} else {
			hpPercent += buff.Value
		}
	}
	dice := ActiveDiceBonuses(c, now)
	hpPercent += dice.HPPercent
	mpPercent += dice.MPPercent
	hp += int(int64(hp) * int64(hpPercent) / 100)
	mp += int(int64(mp) * int64(mpPercent) / 100)
	// Java PlayerStats then adds the flat ENHANCED_MAXHP/MP and indie
	// HP_BOOST/MP_BOOST buffs (getCurrentMaxHp/Mp).
	for _, buff := range c.SkillBuffs {
		if !buff.ExpiresAt.After(now) || buff.Value < 0 || buff.Value > 99999 {
			continue
		}
		switch buff.Stat {
		case "ENHANCED_MAXHP", "HP_BOOST":
			hp += buff.Value
		case "ENHANCED_MAXMP", "MP_BOOST":
			mp += buff.Value
		}
	}
	if fury, ok := DemonFuryMax(c.Job); ok {
		mp = fury // Java getMPByJob replaces every MaxMP bonus.
	}
	// Bases above a lowered ceiling (long vitals switched off) stay valid and
	// only the effective maxima are capped.
	return min(domain.MaxVital(), hp), min(domain.MaxVital(), mp), nil
}

// DemonFuryMax is the Demon Slayer's fixed Demon Fury maximum (Java
// getMPByJob); it replaces MaxMP.
func DemonFuryMax(job int) (int, bool) { return domain.DemonFuryMax(job) }
