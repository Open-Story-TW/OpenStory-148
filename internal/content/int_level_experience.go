package content

import (
	"math"
	"sync"

	"maple-go-server/internal/domain"
)

// The bands above level 199 match the launcher's table, so both
// ends agree on every requirement. Above the last band the requirement stays
// flat; the client resolves those levels to the same row.
const lastDefinedExperienceLevel = 219

var extendedExperience = sync.OnceValue(func() [lastDefinedExperienceLevel + 1]int64 {
	var table [lastDefinedExperienceLevel + 1]int64
	copy(table[:], levelExperience)
	table[200] = int64(float64(table[199]) * 1.5) // truncated, as in the client
	grow := func(first, last int, multiplier float64) {
		for level := first; level <= last; level++ {
			table[level] = int64(math.Floor(float64(table[level-1])*multiplier + 0.5))
		}
	}
	grow(201, 209, 1.2)
	grow(210, lastDefinedExperienceLevel, 1.06)
	return table
})

// maxIntExperienceRequirement matches the client. A requirement must stay
// reachable: EXP on the wire is an int, and a grant that would pass its maximum
// never settles, so one equal to that maximum could never be met.
const maxIntExperienceRequirement = 2_000_000_000

// intLevelExperience is the requirement above the stock table.
func intLevelExperience(level int) int64 {
	requirement := extendedExperience()[min(level, lastDefinedExperienceLevel)]
	if domain.LongExperienceEnabled() {
		return requirement
	}
	return min(requirement, maxIntExperienceRequirement)
}

func experienceAboveStockTable(level int) int64 {
	if level >= len(levelExperience) && domain.IntLevelEnabled() {
		return intLevelExperience(level)
	}
	return math.MaxInt32
}
