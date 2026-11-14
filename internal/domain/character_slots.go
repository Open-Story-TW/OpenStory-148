package domain

import "sync/atomic"

// MaxCharacterSlots is the most characters an account may hold in one world.
// The v148 client keeps fifteen; the launcher widens its list to sixty.
const MaxCharacterSlots = 60

// DefaultCharacterSlots is the limit without a saved setting: the launcher's
// full list. A client without the launcher holds only fifteen.
const DefaultCharacterSlots = MaxCharacterSlots

var characterSlots atomic.Int32

func init() { characterSlots.Store(DefaultCharacterSlots) }

// CharacterSlots is how many characters an account may create in one world.
// Lowering it never hides or locks characters an account already has; it only
// stops new ones until the account is back under the limit.
func CharacterSlots() int { return int(characterSlots.Load()) }

// SetCharacterSlots changes the creation limit, clamped to 1..MaxCharacterSlots.
func SetCharacterSlots(n int) {
	characterSlots.Store(int32(min(max(n, 1), MaxCharacterSlots)))
}

// SetCharacterSlotsForTest changes the limit and returns a restore function.
func SetCharacterSlotsForTest(n int) (restore func()) {
	previous := characterSlots.Load()
	SetCharacterSlots(n)
	return func() { characterSlots.Store(previous) }
}

// AdvertisedCharacterSlots is the slot count a character list announces for
// an account that holds count characters: the creation limit, or the number
// of characters when an earlier, higher limit left the account above it, so
// every character still shows and the client offers no empty slot.
func AdvertisedCharacterSlots(count int) int {
	return min(max(CharacterSlots(), count), MaxCharacterSlots)
}
