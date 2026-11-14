package domain

import (
	"os"
	"sync/atomic"
)

// NativeVitalLimit is the stock client's highest max HP and MP.
const NativeVitalLimit = 99_999

// VitalCeiling is the highest max HP or MP with long vitals on, and the
// highest base value the database accepts. The client's percent bonuses are
// 32-bit max*percent/100, so this keeps bonuses up to 214% from overflowing
// there. It matches the launcher's limit.
const VitalCeiling = 9_999_999

// longVitals raises max HP and MP to VitalCeiling. It is on unless
// MAPLEGO_LONG_VITALS=0. HP and MP are already four bytes on the wire; the
// launcher learns the matching client cap from 0x612 bit 7.
var longVitals atomic.Bool

func init() { longVitals.Store(os.Getenv("MAPLEGO_LONG_VITALS") != "0") }

// LongVitalsEnabled reports whether max HP and MP may exceed 99,999.
func LongVitalsEnabled() bool { return longVitals.Load() }

// SetLongVitals selects the max HP and MP ceiling.
func SetLongVitals(enabled bool) { longVitals.Store(enabled) }

// SetLongVitalsForTest switches the ceiling and returns a restore function.
func SetLongVitalsForTest(enabled bool) (restore func()) {
	previous := longVitals.Swap(enabled)
	return func() { longVitals.Store(previous) }
}

// MaxVital is the highest max HP or MP the active contract allows.
func MaxVital() int {
	if LongVitalsEnabled() {
		return VitalCeiling
	}
	return NativeVitalLimit
}
