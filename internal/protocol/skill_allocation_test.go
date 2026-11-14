package protocol

import "testing"

func TestReadSkillAllocationTakesAnOptionalCount(t *testing.T) {
	native := []byte{1, 0, 0, 0, 0xE0, 0xC8, 0x10, 0}
	v, err := ReadSkillAllocation(NewReader(native))
	if err != nil || v.Tick != 1 || v.SkillID != 1100000 || v.Count != 1 {
		t.Fatalf("native %+v %v", v, err)
	}
	v, err = ReadSkillAllocation(NewReader(append(append([]byte{}, native...), 20, 0, 0, 0)))
	if err != nil || v.SkillID != 1100000 || v.Count != 20 {
		t.Fatalf("counted %+v %v", v, err)
	}
	for _, tail := range [][]byte{{0, 0, 0, 0}, {0xFF, 0xFF, 0xFF, 0xFF}, {101, 0, 0, 0}, {1}, {1, 0, 0, 0, 0}} {
		if _, err = ReadSkillAllocation(NewReader(append(append([]byte{}, native...), tail...))); err == nil {
			t.Fatalf("accepted tail %x", tail)
		}
	}
}
