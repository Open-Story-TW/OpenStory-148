package protocol

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestV148DiceOutcomeUsesBit114AndItsNativeVector(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		value int
		want  map[int]int32
	}{
		{2, map[int]int32{8: 30}},
		{3, map[int]int32{0: 20, 1: 20}},
		{4, map[int]int32{2: 15}},
		{5, map[int]int32{12: 20}},
		{6, map[int]int32{17: 30}},
		{46, map[int]int32{2: 15, 17: 30}},
		{500, map[int]int32{12: 30}},
	} {
		row := domain.SkillBuff{ID: 5711011, Level: 20, Value: tc.value, Stat: "DICE_ROLL", ExpiresAt: now.Add(time.Second)}
		r := NewReader(SkillBuff(row, now))
		if r.U16() != 0x2d {
			t.Fatal("local temporary-stat opcode")
		}
		for i := 0; i < 8; i++ {
			var mask uint32
			if i == 7-114/32 {
				mask = 1 << (114 % 32)
			}
			if r.U32() != mask {
				t.Fatalf("roll %d has wrong bit114 mask at word %d", tc.value, i)
			}
		}
		if int(r.I16()) != tc.value || r.I32() != 5711011 || r.I32() != 1000 {
			t.Fatalf("roll %d has wrong value, source or duration", tc.value)
		}
		r.Skip(3)
		for i := 0; i < 22; i++ {
			if actual := r.I32(); actual != tc.want[i] {
				t.Fatalf("roll %d array[%d] = %d, want %d", tc.value, i, actual, tc.want[i])
			}
		}
		r.Skip(3)
		if r.Err() != nil || r.Remaining() != 0 {
			t.Fatalf("roll %d had an incomplete or trailing native payload", tc.value)
		}
	}
}

func TestV148DiceAnimationCarriesOriginalSelectors(t *testing.T) {
	for _, tc := range []struct {
		actor, skill int
		roll         domain.SkillDiceRoll
		prefix       int32
	}{
		{0, 5711011, domain.SkillDiceRoll{First: 1}, -1},
		{4911, 5720005, domain.SkillDiceRoll{First: 4, Upgraded: true}, 0},
		{4911, 5720005, domain.SkillDiceRoll{First: 4, Second: 6, Upgraded: true}, -1},
	} {
		r := NewReader(SkillDiceEffect(int64(tc.actor), tc.skill, 5, tc.roll))
		op := r.U16()
		if tc.actor == 0 && op != 0x154 || tc.actor != 0 && (op != 0x144 || r.I32() != int32(tc.actor)) {
			t.Fatalf("wrong effect envelope for actor %d", tc.actor)
		}
		if r.U8() != 3 || r.I32() != int32(tc.roll.First) || r.I32() != tc.prefix || r.I32() != int32(tc.skill) || r.U8() != 5 || r.U8() != 0 || r.Err() != nil || r.Remaining() != 0 {
			t.Fatalf("wrong native dice effect for %+v", tc)
		}
	}
}
