package domain

import (
	"os"
	"sync/atomic"
)

// stackBuffs is on unless MAPLEGO_STACK_BUFFS=0. While it is on, the
// additive stats (attack, magic attack, accuracy, avoidability, speed, jump)
// of different skills and items stack: each source keeps its own row and the
// client receives them as native indie entries, which it sums. A recast of
// the same source still refreshes its own row. While it is off a stat lives
// in one source only and the newest cast replaces the older one, as the
// stock game does.
var stackBuffs atomic.Bool

func init() { stackBuffs.Store(os.Getenv("MAPLEGO_STACK_BUFFS") != "0") }

// StackBuffsEnabled reports whether additive buffs of different sources stack.
func StackBuffsEnabled() bool { return stackBuffs.Load() }

// SetStackBuffsForTest switches the setting and returns a restore function.
func SetStackBuffsForTest(on bool) (restore func()) {
	previous := stackBuffs.Swap(on)
	return func() { stackBuffs.Store(previous) }
}
