package skills

import (
	"fmt"
	"math"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// PotionRecoveryModifiersChecked prepares only the reviewed item consumer.
// ResolvePotentialEffects still checks unknown state/option/band; unrelated
// pending procs are not guessed, nor do they disable an independent item use.
func PotionRecoveryModifiersChecked(c domain.Character, now time.Time) (content.PotionRecoveryModifiers, error) {
	m := content.PotionRecoveryModifiers{}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	effects, err := content.ResolvePotentialEffects(root, c, now)
	if err != nil {
		return m, err
	}
	percent := int64(effects.Values["RecoveryUP"])
	seen := map[int]bool{}
	for _, row := range c.Skills {
		eligible := (row.ID == 4110000 || row.ID == 4110014) && (c.Job == 411 || c.Job == 412) ||
			(row.ID == 14110003 || row.ID == 14110011) && (c.Job == 1400 || c.Job == 1410 || c.Job == 1411 || c.Job == 1412) ||
			row.ID == 30000002 && domain.IsResistanceJob(c.Job)
		if !eligible || row.Level < 1 || !row.ExpiresAt.IsZero() && !row.ExpiresAt.After(now) {
			continue
		}
		if seen[row.ID] {
			return m, fmt.Errorf("duplicate potion passive %d", row.ID)
		}
		seen[row.ID] = true
		def, e := content.SkillDefinition(root, row.ID)
		if e != nil || def.Disabled || row.Level > def.MaxLevel {
			return m, fmt.Errorf("invalid potion passive %d: %v", row.ID, e)
		}
		// Consume the actual Java job-switch sources, including a preexisting
		// Night Walker row on1400/1410. Allocation eligibility is a separate
		// command; applying it here would change the preserved-source policy.
		values, e := def.Values(EffectiveLevel(c, row.ID, now))
		if e != nil || values["x"] < 100 {
			return m, fmt.Errorf("unsupported potion passive %d", row.ID)
		}
		percent += int64(values["x"]) - 100
	}
	if percent < 0 || percent > math.MaxInt32-100 {
		return m, fmt.Errorf("invalid potion recovery aggregate")
	}
	m.FixedHPPercent = int(percent)
	return m, nil
}
