package domain

import (
	"math"
	"testing"
)

func TestRateExperience(t *testing.T) {
	t.Cleanup(func() { SetRates(1, 1, 1) })
	if got := RateExperience(100); got != 100 {
		t.Fatalf("rate 1: %d", got)
	}
	SetRates(5, 0, 5000)
	if ExpRate() != 5 || MesoRate() != 1 || DropRate() != MaxRate {
		t.Fatalf("rates not clamped: %d %d %d", ExpRate(), MesoRate(), DropRate())
	}
	if got := RateExperience(100); got != 500 {
		t.Fatalf("rate 5: %d", got)
	}
	if got := RateExperience(0); got != 0 {
		t.Fatalf("zero grant: %d", got)
	}
	restore := SetLongExperienceForTest(false)
	defer restore()
	if got := RateExperience(math.MaxInt32 - 1); got != math.MaxInt32 {
		t.Fatalf("grant must stop at the contract maximum: %d", got)
	}
}
