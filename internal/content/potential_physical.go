package content

import (
	"fmt"
	"time"

	"maple-go-server/internal/domain"
)

// PotentialPhysicalMaximum is a precomputed, pure bound for one authoritative
// mob. Preparing may read immutable WZ; calling it never loads data or acquires
// a repository/map lock. Skill registry installs the checked combat adapter.
type PotentialPhysicalMaximum func(m Mob, skillID, damagePercent int) (int, error)

var PotentialPhysicalBoundsProvider func(c domain.Character, now time.Time) (PotentialPhysicalMaximum, error)

// PreparePotentialPhysicalMaximum shares one checked contract between legacy
// world admission and Store revalidation. Nil denotes the original unchanged
// no-potential policy, not an unchecked active-equipment fallback.
func PreparePotentialPhysicalMaximum(root string, c domain.Character, now time.Time) (PotentialPhysicalMaximum, error) {
	effects, err := ResolvePotentialEffects(root, c, now)
	if err != nil {
		return nil, err
	}
	if len(effects.Pending) > 0 {
		return nil, &PendingPotentialEffectsError{Effects: effects.Pending}
	}
	if len(effects.Lines) == 0 {
		return nil, nil
	}
	if PotentialPhysicalBoundsProvider == nil {
		return nil, fmt.Errorf("checked potential physical provider unavailable")
	}
	maximum, err := PotentialPhysicalBoundsProvider(c, now)
	if err != nil {
		return nil, err
	}
	if maximum == nil {
		return nil, fmt.Errorf("checked potential provider returned no bound")
	}
	return maximum, nil
}
