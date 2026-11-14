package domain

import "context"

const KeymapSlots = 89

type KeyBinding struct {
	Type   uint8 `json:"type"`
	Action int32 `json:"action"`
}
type Keymap [KeymapSlots]KeyBinding
type KeyChange struct {
	Key     int
	Binding KeyBinding
}

// These are Legacy MapleCharacter.saveNewChar's three parallel arrays.
// A new server character must not inherit an unrelated local client's map.
func DefaultKeymap() Keymap {
	keys := [...]int{18, 65, 2, 23, 3, 4, 5, 6, 16, 17, 19, 25, 26, 27, 31, 34, 35, 37, 38, 40, 43, 44, 45, 46, 50, 56, 59, 60, 61, 62, 63, 64, 57, 48, 29, 7, 24, 33, 41, 39, 8, 20, 21, 49}
	types := [...]uint8{4, 6, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5, 4, 4, 5, 6, 6, 6, 6, 6, 6, 5, 4, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4}
	actions := [...]int32{0, 106, 10, 1, 12, 13, 18, 24, 8, 5, 4, 19, 14, 15, 2, 17, 11, 3, 20, 16, 9, 50, 51, 6, 7, 53, 100, 101, 102, 103, 104, 105, 54, 30, 52, 21, 25, 26, 23, 27, 29, 28, 31, 22}
	var result Keymap
	for i, key := range keys {
		result[key] = KeyBinding{types[i], actions[i]}
	}
	return result
}

func ValidKeyBinding(b KeyBinding) bool {
	return b.Type <= 8 && b.Action >= 0 && (b.Type != 0 || b.Action == 0)
}

type KeymapRepository interface {
	ReadKeymap(context.Context, CharacterSession) (Keymap, error)
	UpdateKeymap(context.Context, CharacterSession, []KeyChange) error
}
