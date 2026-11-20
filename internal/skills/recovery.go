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

const (
	durabilitySkill       = 5100013
	bloodCirculationSkill = 5700005
)

func IsReviewedRecoverySkill(id int) bool {
	return id == 1110000 || id == 11110000 || id == 51110000 || id == durabilitySkill || id == bloodCirculationSkill
}

func reviewedRecoveryJob(id, job int) bool {
	switch id {
	case 1110000:
		return job == 111 || job == 112
	case 11110000:
		return job == 1111 || job == 1112
	case 51110000:
		return job == 5111 || job == 5112
	case durabilitySkill:
		return job >= 510 && job <= 512
	case bloodCirculationSkill:
		return job >= 570 && job <= 572
	default:
		return false
	}
}

// ReviewedRecoveryTick applies the eligible job's WZ HP and MP recovery.
// Native ordinary regeneration requests do not include these skill bonuses.
func ReviewedRecoveryTick(c *TriggerContext) *BuffChange {
	if c == nil || c.Character.HP <= 0 {
		return nil
	}
	id, job := c.Skill.ID, c.Character.Job
	if !reviewedRecoveryJob(id, job) {
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
	if hp > 0 {
		change.HealHP = 10 + hp
	}
	return change
}

// ReviewedRecoveryPlans computes the server's passive recovery ticks.
// Potential recovery uses its own native request and cadence.
func ReviewedRecoveryPlans(c domain.Character, now time.Time) ([]PeriodicRecoveryPlan, error) {
	var out []PeriodicRecoveryPlan
	seen := map[int]bool{}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	for _, state := range c.Skills {
		if !IsReviewedRecoverySkill(state.ID) || state.Level <= 0 || !state.ExpiresAt.IsZero() && !state.ExpiresAt.After(now) {
			continue
		}
		if !reviewedRecoveryJob(state.ID, c.Job) {
			continue
		}
		if seen[state.ID] {
			return nil, fmt.Errorf("duplicate periodic recovery source")
		}
		seen[state.ID] = true
		def, err := content.SkillDefinition(root, state.ID)
		if err != nil || def.Disabled || !def.CanLearnBy(c) || state.Level > def.MaxLevel {
			return nil, fmt.Errorf("invalid reviewed recovery skill")
		}
		values, err := def.Values(state.Level)
		if err != nil {
			return nil, err
		}
		if state.ID == durabilitySkill || state.ID == bloodCirculationSkill {
			percent, seconds := values["x"], values["y"]
			if percent < 1 || percent > 100 || seconds < 1 || seconds > 86400 {
				return nil, fmt.Errorf("invalid percentage recovery values")
			}
			maxHP, maxMP := c.MaxHP, c.MaxMP
			if c.VitalsModel == 1 {
				maxHP, maxMP, err = content.DerivedVitals(c, now)
				if err != nil {
					return nil, err
				}
			}
			hp, mp, err := content.ScaleReviewedRecovery(root, c.MapID, int(int64(maxHP)*int64(percent)/100), int(int64(maxMP)*int64(percent)/100))
			if err != nil {
				return nil, err
			}
			out = append(out, PeriodicRecoveryPlan{SkillID: state.ID, HP: hp, MP: mp, Interval: time.Duration(seconds) * time.Second})
			continue
		}
		change := ReviewedRecoveryTick(&TriggerContext{Skill: def, Level: state.Level, Values: values, Character: c, Now: now})
		if change == nil {
			return nil, fmt.Errorf("periodic recovery effective stats unresolved")
		}
		hp, mp, err := content.ScaleReviewedRecovery(root, c.MapID, change.HealHP, change.HealMP)
		if err != nil {
			return nil, err
		}
		out = append(out, PeriodicRecoveryPlan{SkillID: state.ID, HP: hp, MP: mp, Interval: 4 * time.Second})
	}
	return out, nil
}
