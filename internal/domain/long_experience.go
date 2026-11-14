package domain

import (
	"math"
	"os"
	"sync/atomic"
)

// longExperience selects the 64-bit EXP contract, on unless
// MAPLEGO_LONG_EXPERIENCE=0. It changes packet layouts; the launcher takes it
// from the client config packet.
var longExperience atomic.Bool

func init() { longExperience.Store(os.Getenv("MAPLEGO_LONG_EXPERIENCE") != "0") }

// LongExperienceEnabled reports whether EXP is 64-bit on the wire.
func LongExperienceEnabled() bool { return longExperience.Load() }

// SetLongExperienceForTest switches the contract and returns a restore function.
func SetLongExperienceForTest(enabled bool) (restore func()) {
	previous := longExperience.Swap(enabled)
	return func() { longExperience.Store(previous) }
}

// MaxExperience is the highest EXP value or single grant the contract carries.
func MaxExperience() int64 {
	if LongExperienceEnabled() {
		return math.MaxInt64
	}
	return math.MaxInt32
}

// AddExperience adds two non-negative amounts, stopping at MaxExperience.
func AddExperience(a, b int64) int64 {
	maximum := MaxExperience()
	if b > maximum-a {
		return maximum
	}
	return a + b
}
