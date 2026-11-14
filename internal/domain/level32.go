package domain

import (
	"os"
	"sync/atomic"
)

// level32 selects the launcher's 32-bit level contract for GW_CharacterStat
// and the level stat update. The launcher always installs its level32 decode
// independent of IntLevel, so it is on unless MAPLEGO_LEVEL32=0, which only a stock client without the
// launcher needs. The int-level feature always keeps it on.
var level32 atomic.Bool

func init() { level32.Store(os.Getenv("MAPLEGO_LEVEL32") != "0") }

// Level32Wire reports whether character levels go on the wire as i32.
func Level32Wire() bool { return level32.Load() || IntLevelEnabled() }

// SetLevel32ForTest switches the contract and returns a restore function.
func SetLevel32ForTest(enabled bool) (restore func()) {
	previous := level32.Swap(enabled)
	return func() { level32.Store(previous) }
}
