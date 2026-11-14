package skills

import (
	"fmt"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// ResourceModifiers resolves only resource consumers. Other pending potential
// effects remain pending in the attack/stat provider; this does not claim them
// implemented. No caller may run this WZ-reading phase under a room mutex.
func ResourceModifiers(c domain.Character, now time.Time) (content.ResourceCostModifiers, error) {
	m := content.ResourceCostModifiers{Prepared: true, MPConsumePercent: 100}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	p, err := content.ResolvePotentialEffects(root, c, now)
	if err != nil {
		return m, err
	}
	m.MPReductionPercent = p.Values["mpconReduce"]
	m.CooldownReductionSeconds = p.Values["reduceCooltime"]
	_, m.DemonForce = content.DemonFuryMax(c.Job)
	if value, ok := ActiveStat(c, "INFINITY", now); ok {
		m.Infinity = value != 0
	}
	if value, ok := ActiveStat(c, "BOUNDLESS_RAGE", now); ok {
		m.BoundlessRage = value > 0
	}
	// These are the four original Java job-switch sources of mpconPercent.
	// Read them checked rather than using Bonuses' compatibility skip-on-error.
	for _, state := range c.Skills {
		if state.Level <= 0 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
			continue
		}
		switch state.ID {
		case 2110001, 2210001, 12110001, 22150000:
		default:
			continue
		}
		def, e := content.SkillDefinition(root, state.ID)
		if e != nil {
			return m, e
		}
		if !def.CanLearnBy(c) {
			continue
		}
		values, e := def.Values(EffectiveLevel(c, state.ID, now))
		if e != nil {
			return m, e
		}
		m.MPConsumePercent += values["x"] - 100
	}
	seen := map[string]bool{}
	for _, row := range c.SkillBuffs {
		if !row.ExpiresAt.After(now) || row.Stat != "CONCENTRATE" && row.Stat != "HOLY_SHIELD" && row.Stat != "TELEPORT_MASTERY" && row.Stat != "MECH_CHANGE" && row.Stat != "INVINCIBILITY" {
			continue
		}
		if seen[row.Stat] {
			return m, fmt.Errorf("duplicate resource buff %s", row.Stat)
		}
		seen[row.Stat] = true
		switch row.Stat {
		case "CONCENTRATE":
			m.MPReductionPercent += row.Value
			continue
		case "TELEPORT_MASTERY":
			m.TeleportExtraMP = max(0, row.Value)
			continue
		case "MECH_CHANGE":
			m.MechanicMode = row.Value > 0
			continue
		case "INVINCIBILITY":
			if row.Value == 0 || c.Job/1000 != 2 {
				continue
			}
			source := 10000*nativeHeroBeginner(c.Job) + 93
			level := EffectiveLevel(c, source, now)
			if level < 1 {
				continue
			}
			def, e := content.SkillDefinition(root, source)
			if e != nil {
				return m, e
			}
			values, e := def.Values(level)
			if e != nil {
				return m, e
			}
			m.MPReductionPercent += values["x"]
			continue
		}
		if row.Value == 0 {
			continue
		}
		def, e := content.SkillDefinition(root, row.ID)
		if e != nil {
			return m, e
		}
		values, e := def.Values(row.Level)
		if e != nil {
			return m, e
		}
		m.MPReductionPercent += values["mpConReduce"]
	}
	if m.MechanicMode {
		for i, id := range []int{35111004, 35121005} {
			level := EffectiveLevel(c, id, now)
			if level < 1 {
				continue
			}
			def, e := content.SkillDefinition(root, id)
			if e != nil {
				return m, e
			}
			values, e := def.Values(level)
			if e != nil {
				return m, e
			}
			m.MechanicMP[i] = values["w"]
		}
		m.MechanicMP[2] = m.MechanicMP[1]
	}
	if _, err = content.CalculateResourceCost(0, 0, 0, m); err != nil {
		return m, err
	}
	return m, nil
}

// ResolveResourceCost is shared by self/party casts, legacy attacks and Store
// revalidation. The amount is the skill handler's unmodified MP/DF cost.
func ResolveResourceCost(c domain.Character, skillID, mp int, cooldown time.Duration, now time.Time) (content.ResourceCost, error) {
	m, err := ResourceModifiers(c, now)
	if err != nil {
		return content.ResourceCost{}, err
	}
	return content.CalculateResourceCost(skillID, mp, cooldown, m)
}

// Native659841/463F68/463F88/4C8B64; distinct from Java's legacy job aliases.
func nativeHeroBeginner(job int) int {
	switch {
	case job/100 == 22 || job == 2001:
		return 2001
	case job/100 == 23 || job == 2002:
		return 2002
	case job/100 == 24 || job == 2003:
		return 2003
	}
	return 1000 * (job / 1000)
}

// CastResourceCost keeps the raw handler plan separate from resource kind.
// Java Demon Slayer ignores ordinary mpCon and only pays forceCon. A custom
// zero-cost cast remains zero (e.g. Demon Chase's already-paid teleport).
func CastResourceCost(c domain.Character, id int, plan CastPlan, now time.Time) (content.ResourceCost, error) {
	mp := plan.MPCost
	if _, demon := content.DemonFuryMax(c.Job); demon && mp > 0 {
		def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
		if err != nil {
			return content.ResourceCost{}, err
		}
		levelID := id
		if h, ok := Lookup(id); ok && h.LevelFrom != 0 {
			levelID = h.LevelFrom
		}
		level := LinkedLevel(c, levelID, id, now)
		if level < 1 {
			return content.ResourceCost{}, fmt.Errorf("force cost requires learned source")
		}
		values, err := def.Values(level)
		if err != nil {
			return content.ResourceCost{}, err
		}
		if values["forceCon"] == 0 {
			mp = 0
		} else if mp == values["mpCon"]+values["forceCon"] {
			mp = values["forceCon"]
		}
	}
	return ResolveResourceCost(c, id, mp, plan.Cooldown, now)
}
