package domain

import "testing"

func TestCharacterSlotsClampAndAdvertise(t *testing.T) {
	defer SetCharacterSlotsForTest(DefaultCharacterSlots)()
	for _, tc := range []struct{ set, want int }{{0, 1}, {15, 15}, {60, 60}, {61, 60}} {
		SetCharacterSlots(tc.set)
		if got := CharacterSlots(); got != tc.want {
			t.Fatalf("SetCharacterSlots(%d) -> %d want %d", tc.set, got, tc.want)
		}
	}
	SetCharacterSlots(10)
	// Under the limit the list offers the limit; above it (the limit was
	// lowered later) it shows every character and no empty slot.
	for _, tc := range []struct{ count, want int }{{0, 10}, {9, 10}, {10, 10}, {25, 25}, {60, 60}} {
		if got := AdvertisedCharacterSlots(tc.count); got != tc.want {
			t.Fatalf("AdvertisedCharacterSlots(%d)=%d want %d", tc.count, got, tc.want)
		}
	}
}
