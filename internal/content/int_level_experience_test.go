package content

import (
	"math"
	"testing"

	"maple-go-server/internal/domain"
)

func TestExtendedExperienceMatchesTheClientBands(t *testing.T) {
	table := extendedExperience()
	if table[199] != levelExperience[199] {
		t.Fatalf("stock row changed: %d", table[199])
	}
	if want := int64(float64(levelExperience[199]) * 1.5); table[200] != want {
		t.Fatalf("level 200 = %d want %d", table[200], want)
	}
	for level := 201; level <= lastDefinedExperienceLevel; level++ {
		if table[level] <= table[level-1] {
			t.Fatalf("level %d does not grow", level)
		}
	}
}

func TestLongExperienceLiftsTheIntRequirementCeiling(t *testing.T) {
	defer domain.SetLongExperienceForTest(true)()
	if got, want := intLevelExperience(999), extendedExperience()[lastDefinedExperienceLevel]; got != want || got <= math.MaxInt32 {
		t.Fatalf("long requirement = %d want %d", got, want)
	}
}

func TestIntLevelExperienceIsFlatAndFitsTheWire(t *testing.T) {
	defer domain.SetLongExperienceForTest(false)()
	for _, level := range []int{219, 220, 256, 999, math.MaxInt32} {
		if got := intLevelExperience(level); got != intLevelExperience(lastDefinedExperienceLevel) {
			t.Fatalf("level %d = %d", level, got)
		}
		if got := intLevelExperience(level); got <= 0 || got > maxIntExperienceRequirement {
			t.Fatalf("level %d out of range: %d", level, got)
		}
	}
	if got := intLevelExperience(200); got > maxIntExperienceRequirement {
		t.Fatalf("level 200 out of range: %d", got)
	}
}
