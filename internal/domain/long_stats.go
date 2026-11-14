package domain

import (
	"os"
	"sync/atomic"
)

// NativeStatLimit is the highest base stat the 16-bit wire contract carries.
const NativeStatLimit = 32767

// longStatLimit is the highest base stat with long stats on. The client's
// percent bonuses are 32-bit stat*percent/100, so this keeps bonuses up to 214%
// from overflowing there. It matches the launcher's limit.
const longStatLimit = 9_999_999

// longStats selects four-byte STR, DEX, INT, LUK and AP on the wire. It is on
// unless MAPLEGO_LONG_STATS=0, and must match the client's open.ini [Features]
// LongStats, because it changes packet layouts.
var longStats atomic.Bool

func init() { longStats.Store(os.Getenv("MAPLEGO_LONG_STATS") != "0") }

// LongStatsEnabled reports whether base stats and AP are four bytes on the wire.
func LongStatsEnabled() bool { return longStats.Load() }

// SetLongStatsForTest switches the contract and returns a restore function.
func SetLongStatsForTest(enabled bool) (restore func()) {
	previous := longStats.Swap(enabled)
	return func() { longStats.Store(previous) }
}

// MaxStat is the highest base stat or AP the active contract carries.
func MaxStat() int {
	if LongStatsEnabled() {
		return longStatLimit
	}
	return NativeStatLimit
}
