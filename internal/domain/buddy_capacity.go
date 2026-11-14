package domain

import "sync/atomic"

// Friend list size. A character's capacity is its stored value (232 for a
// new character, as in Legacy) raised to the operator's buddy_capacity, so the
// setting applies to every character at once.
const (
	// NativeBuddyCapacity is the ceiling without the large buddy contract:
	// the native friend list counts entries in one byte.
	NativeBuddyCapacity = 255
	// DefaultBuddyCapacityLimit is the buddy_capacity default.
	DefaultBuddyCapacityLimit = 512
	// MaxBuddyCapacityLimit keeps the friend list packet (61 bytes per
	// friend) inside the 64 KiB packet limit.
	MaxBuddyCapacityLimit = 1000
)

var (
	largeBuddy    atomic.Bool
	buddyCapacity atomic.Int64
)

func init() {
	largeBuddy.Store(true)
	buddyCapacity.Store(DefaultBuddyCapacityLimit)
}

// LargeBuddyEnabled reports whether friend counts are 16-bit on the wire
// (the launcher's LargeBuddy contract).
func LargeBuddyEnabled() bool { return largeBuddy.Load() }

// SetLargeBuddy selects 16-bit friend counts on the wire.
func SetLargeBuddy(enabled bool) { largeBuddy.Store(enabled) }

// SetLargeBuddyForTest switches the contract and returns a restore function.
func SetLargeBuddyForTest(enabled bool) (restore func()) {
	previous := largeBuddy.Swap(enabled)
	return func() { largeBuddy.Store(previous) }
}

// SetBuddyCapacity sets the operator's friend list size.
func SetBuddyCapacity(n int) { buddyCapacity.Store(int64(n)) }

// SetBuddyCapacityForTest changes it and returns a restore function.
func SetBuddyCapacityForTest(n int) (restore func()) {
	previous := buddyCapacity.Swap(int64(n))
	return func() { buddyCapacity.Store(previous) }
}

// BuddyCapacitySetting is the operator's friend list size.
func BuddyCapacitySetting() int { return int(buddyCapacity.Load()) }

// MaxWireBuddies is the most friends the wire contract can carry.
func MaxWireBuddies() int {
	if LargeBuddyEnabled() {
		return MaxBuddyCapacityLimit
	}
	return NativeBuddyCapacity
}

// EffectiveBuddyCapacity is the friend list size of a character whose stored
// capacity is stored.
func EffectiveBuddyCapacity(stored int) int {
	return max(1, min(max(stored, BuddyCapacitySetting()), MaxWireBuddies()))
}
