package persistence

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestBonusExperienceMatchesPercentAndRespectsTheCeiling(t *testing.T) {
	restore := domain.SetLongExperienceForTest(false)
	for _, c := range []struct {
		exp     int64
		percent int
		want    int64
	}{{100, 10, 110}, {199, 15, 228}, {7, 33, 9}, {0, 50, 0}, {50, 0, 50}, {2147483647, 10, 2147483647}} {
		if got := bonusExperience(c.exp, c.percent); got != c.want {
			t.Errorf("bonusExperience(%d, %d) = %d, want %d", c.exp, c.percent, got, c.want)
		}
	}
	restore()

	defer domain.SetLongExperienceForTest(true)()
	if got := bonusExperience(5_000_000_000, 10); got != 5_500_000_000 {
		t.Errorf("64-bit grant: %d", got)
	}
	if got := bonusExperience(domain.MaxExperience(), 50); got != domain.MaxExperience() {
		t.Errorf("64-bit ceiling: %d", got)
	}
}
