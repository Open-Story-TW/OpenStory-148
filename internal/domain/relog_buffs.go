package domain

import (
	"context"
	"os"
	"sync/atomic"
)

// relogBuffs is on unless MAPLEGO_RELOG_BUFFS=0. While it is on, saved buffs
// and their summons come back on a fresh login with the time they have left.
// While it is off a fresh login drops every saved buff, as the stock game
// does. Channel changes and cash shop returns keep buffs either way.
var relogBuffs atomic.Bool

func init() {
	relogBuffs.Store(os.Getenv("MAPLEGO_RELOG_BUFFS") != "0")
}

// RelogBuffsEnabled reports whether a fresh login keeps saved buffs.
func RelogBuffsEnabled() bool { return relogBuffs.Load() }

// SetRelogBuffsForTest switches the setting and returns a restore function.
func SetRelogBuffsForTest(on bool) (restore func()) {
	previous := relogBuffs.Swap(on)
	return func() { relogBuffs.Store(previous) }
}

// SkillBuffClearer drops every saved skill buff of an owned character in one
// transaction, settling buff-dependent maxima like an expiry.
type SkillBuffClearer interface {
	ClearSkillBuffs(ctx context.Context, session CharacterSession) (SkillResult, error)
}
