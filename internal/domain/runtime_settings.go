package domain

import "sync/atomic"

// The setters below belong to internal/settings, which applies the saved
// server settings over the environment defaults. The wire-contract switches
// (int level, long stats, long experience) are applied once, before a service
// accepts connections; the rest may change while players are online.

// SetIntLevelCap sets the level cap; 0 restores the stock limits.
func SetIntLevelCap(limit int) { intLevelCap.Store(int64(limit)) }

// SetLongStats selects four-byte base stats on the wire.
func SetLongStats(enabled bool) { longStats.Store(enabled) }

// SetLongExperience selects 64-bit EXP on the wire.
func SetLongExperience(enabled bool) { longExperience.Store(enabled) }

// SetStackBuffs lets additive buffs of different sources stack.
func SetStackBuffs(enabled bool) { stackBuffs.Store(enabled) }

// SetRelogBuffs keeps saved buffs across a fresh login.
func SetRelogBuffs(enabled bool) { relogBuffs.Store(enabled) }

// MaxRate bounds every rate multiplier.
const MaxRate = 1000

var expRate, mesoRate, dropRate atomic.Int64

func init() { SetRates(1, 1, 1) }

func clampRate(v int) int64 { return int64(min(max(v, 1), MaxRate)) }

// SetRates sets the EXP, meso and drop multipliers, each kept in 1..MaxRate.
func SetRates(exp, meso, drop int) {
	expRate.Store(clampRate(exp))
	mesoRate.Store(clampRate(meso))
	dropRate.Store(clampRate(drop))
}

// ExpRate multiplies the EXP of a defeated mob.
func ExpRate() int64 { return expRate.Load() }

// MesoRate multiplies the mesos a mob drops.
func MesoRate() int64 { return mesoRate.Load() }

// DropRate multiplies the chance of a mob's item drops.
func DropRate() int64 { return dropRate.Load() }

// RateExperience applies the EXP rate to one grant, stopping at MaxExperience.
func RateExperience(exp int64) int64 {
	rate := ExpRate()
	if exp <= 0 || rate <= 1 {
		return exp
	}
	if exp > MaxExperience()/rate {
		return MaxExperience()
	}
	return exp * rate
}
