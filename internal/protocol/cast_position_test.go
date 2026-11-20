package protocol

import (
	"encoding/hex"
	"testing"
)

// The v148 predicate 0x6AC385 includes skills the v145-derived list missed.
func TestCastHasPositionFollowsV148(t *testing.T) {
	for _, id := range []int{14001007, 14001003, 15101008, 4301003, 5711011, 5821009, 5911007, 20031209, 24121008, 51111004, 2311001, 1121000} {
		if !castHasPosition(id) {
			t.Errorf("%d should carry the caster position", id)
		}
	}
	for _, id := range []int{14101003, 1001004, 2301002, 13101006, 22131001, 51121006} {
		if castHasPosition(id) {
			t.Errorf("%d should not carry a position", id)
		}
	}
}

// A captured v148 Haste (14001007) party cast: tick, skill, level 20, X/Y,
// the affected-member mask, delay 0 and the trailing zero.
func TestReadSkillCastHastePartyCast(t *testing.T) {
	raw, _ := hex.DecodeString("cb9dd90c6fa3d5001415ffd70038000000")
	v, err := ReadSkillCast(NewReader(raw))
	if err != nil {
		t.Fatalf("Haste cast rejected: %v", err)
	}
	if v.SkillID != 14001007 || v.Level != 20 || !v.HasPosition || v.X != -235 || v.Y != 215 || v.Affected != 0x38 {
		t.Fatalf("Haste cast %+v", v)
	}
}
