package skills

import (
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"os"
	"time"
)

type PeriodicRecoveryPlan struct {
	SkillID, HP, MP int
	Interval        time.Duration
}

func IsReviewedRecoverySkill(id int) bool { return id == 1110000 || id == 11110000 }

// ReviewedRecoveryTick follows Java PlayerStats.relocHeal's exact job switch.
// Cygnus's WZ hp field is deliberately not invented into a supported effect:
// this Java branch consumes only mp; native generic skill-side HP remains UAT.
func ReviewedRecoveryTick(c *TriggerContext) *BuffChange {
	if c == nil || c.Character.HP <= 0 {
		return nil
	}
	id, job := c.Skill.ID, c.Character.Job
	hero := id == 1110000 && (job == 111 || job == 112)
	cygnus := id == 11110000 && (job == 1111 || job == 1112)
	if !hero && !cygnus {
		return nil
	}
	stats, err := PlayerStatsChecked(c.Character, c.Now)
	if err != nil || stats.INT < 0 {
		return nil
	}
	hp, mp := c.Values["hp"], c.Values["mp"]
	if hp < 0 || mp < 0 {
		return nil
	}
	change := &BuffChange{HealMP: 3 + stats.INT/10 + mp}
	if hero && hp > 0 {
		change.HealHP = 10 + hp
	}
	return change
}

// ReviewedRecoveryPlans is a fresh, pure policy for the separate4s server
// source. It never sums potential RecoveryHP/MP into this source. Those arrive
// on native0x92 and retain their independent component cadence.
func ReviewedRecoveryPlans(c domain.Character, now time.Time) ([]PeriodicRecoveryPlan, error) {
	var out []PeriodicRecoveryPlan
	seen := map[int]bool{}
	for _, state := range c.Skills {
		if !IsReviewedRecoverySkill(state.ID) || state.Level <= 0 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
			continue
		}
		eligible := state.ID == 1110000 && (c.Job == 111 || c.Job == 112) || state.ID == 11110000 && (c.Job == 1111 || c.Job == 1112)
		if !eligible {
			continue
		}
		if seen[state.ID] {
			return nil, fmt.Errorf("duplicate periodic recovery source")
		}
		seen[state.ID] = true
		def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), state.ID)
		if err != nil || def.Disabled || !def.CanLearnBy(c) || state.Level > def.MaxLevel {
			return nil, fmt.Errorf("invalid reviewed recovery skill")
		}
		values, err := def.Values(state.Level)
		if err != nil {
			return nil, err
		}
		change := ReviewedRecoveryTick(&TriggerContext{Skill: def, Level: state.Level, Values: values, Character: c, Now: now})
		if change == nil {
			return nil, fmt.Errorf("periodic recovery effective stats unresolved")
		}
		hp, mp, err := content.ScaleReviewedRecovery(os.Getenv("MAPLEGO_WZ_ROOT"), c.MapID, change.HealHP, change.HealMP)
		if err != nil {
			return nil, err
		}
		out = append(out, PeriodicRecoveryPlan{SkillID: state.ID, HP: hp, MP: mp, Interval: 4 * time.Second})
	}
	return out, nil
}
