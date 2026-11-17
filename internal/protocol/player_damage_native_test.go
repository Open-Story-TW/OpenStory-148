package protocol

import (
	"encoding/hex"
	"testing"
)

func TestNativeContactDamageRequiresFinalFlagByte(t *testing.T) {
	// 8893F8/88B1E5: random4,tick4,type1,element1,damage4,context2,
	// monster4,object4,direction1,skill4,reflect4,defense1,guard1,flags1.
	// The final Encode1 runs for both the mob and environmental branches;
	// Java's reader omits it, but this strict decoder must consume it.
	raw, err := hex.DecodeString("0100000002000000ff00050000000000a086010041420f00010000000000000000000000")
	if err != nil || len(raw) != 36 {
		t.Fatalf("fixture: length=%d error=%v", len(raw), err)
	}
	q, err := ReadPlayerDamage(NewReader(raw))
	if err != nil || q.Tick != 2 || q.Type != -1 || q.Damage != 5 || q.MonsterID != 100000 || q.ObjectID != 1000001 || q.Direction != 1 {
		t.Fatalf("native contact rejected/misaligned: %+v %v", q, err)
	}
	for _, n := range []int{0, 16, 25, 34, 35} {
		if _, err := ReadPlayerDamage(NewReader(raw[:n])); err == nil {
			t.Fatalf("truncated native damage accepted: %d", n)
		}
	}
	// defType 1 is Java's Power Guard block (31110008); higher values and
	// the guard/flags paths stay unimplemented.
	blocked := append([]byte(nil), raw...)
	blocked[33] = 1
	if q, err := ReadPlayerDamage(NewReader(blocked)); err != nil || q.DefType != 1 {
		t.Fatalf("defType 1 rejected: %+v %v", q, err)
	}
	for _, c := range []struct {
		at int
		v  byte
	}{{33, 2}, {34, 1}, {35, 1}} {
		changed := append([]byte(nil), raw...)
		changed[c.at] = c.v
		if _, err := ReadPlayerDamage(NewReader(changed)); err == nil {
			t.Fatalf("unimplemented defense/guard/flags path accepted at%d", c.at)
		}
	}
	if _, err := ReadPlayerDamage(NewReader(append(raw, 0))); err == nil {
		t.Fatal("unexplained trailing bytes accepted")
	}
}

func TestNativeFieldObstacleDamage(t *testing.T) {
	raw, err := hex.DecodeString("0100000002000000fd00fa0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	q, err := ReadPlayerDamage(NewReader(raw))
	if err != nil || q.Tick != 2 || q.Type != FieldObstacleDamage || q.Damage != 250 || q.ObjectID != 0 {
		t.Fatalf("native obstacle damage: %+v %v", q, err)
	}
	for _, input := range [][]byte{raw[:len(raw)-1], append(append([]byte(nil), raw...), 0)} {
		if _, err := ReadPlayerDamage(NewReader(input)); err == nil {
			t.Fatalf("accepted invalid obstacle packet length %d", len(input))
		}
	}
	raw[len(raw)-1] = 1
	if _, err := ReadPlayerDamage(NewReader(raw)); err == nil {
		t.Fatal("accepted unreviewed obstacle damage flags")
	}
}
