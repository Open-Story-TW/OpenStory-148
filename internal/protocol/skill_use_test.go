package protocol

import (
	"reflect"
	"testing"
)

func skillUse(id, level int, tail func(*Writer)) SkillCast {
	w := NewRawWriter()
	w.U32(7)
	w.I32(int32(id))
	w.U8(uint8(level))
	if tail != nil {
		tail(w)
	}
	q, _ := ReadSkillCast(NewReader(w.Packet()))
	return q
}

// Layouts follow the native emitter 0x89FF18 (Open-Maple docs/server/baseline/skill-effect-native.json).
func TestSkillUseRequestNativeTails(t *testing.T) {
	q := skillUse(1301007, 20, func(w *Writer) { w.I16(-12); w.I16(34); w.U16(500); w.U8(0) })
	if !q.HasPosition || q.X != -12 || q.Y != 34 || q.ActionDelay != 500 || q.HasMobs {
		t.Fatalf("mass spell %+v", q)
	}
	q = skillUse(1201006, 10, func(w *Writer) {
		w.I16(1)
		w.I16(2)
		w.U8(2)
		w.I32(100)
		w.I32(101)
		w.U16(0)
		w.U8(0)
	})
	if !q.HasPosition || !q.HasMobs || !reflect.DeepEqual(q.Mobs, []int32{100, 101}) {
		t.Fatalf("threaten mob list %+v", q)
	}
	q = skillUse(2311001, 10, func(w *Writer) {
		w.I16(1)
		w.I16(2)
		w.U8(5)
		w.U16(9)
		w.U8(0)
		w.U8(1)
		w.I32(77)
		w.U16(0)
		w.U8(0)
	})
	if q.Affected != 5 || !reflect.DeepEqual(q.Mobs, []int32{77}) {
		t.Fatalf("dispel members and mobs %+v", q)
	}
	q = skillUse(4121006, 1, func(w *Writer) { w.I32(2070000); w.U16(0); w.U8(0) })
	if q.ItemID != 2070000 || q.HasPosition {
		t.Fatalf("item skill %+v", q)
	}
	q = skillUse(1002, 1, func(w *Writer) { w.U16(0); w.U8(0) })
	if q.HasPosition || q.HasMobs || q.SkillID != 1002 {
		t.Fatalf("plain buff %+v", q)
	}
	if q = skillUse(1002, 1, nil); q.SkillID != 1002 {
		t.Fatal("bare request still accepted")
	}
}

func TestSkillUseRequestSummonAndCaptureTails(t *testing.T) {
	// 35111002 Magnetic Field (Tesla Coil): coil 1/2 (8-byte tail) and coil 3 (16-byte tail).
	for _, coils := range []byte{0, 1, 2} {
		w := NewRawWriter()
		w.U32(99)
		w.I32(35111002)
		w.U8(20)
		w.U8(coils)
		if coils == 2 {
			w.I32(5001)
			w.I32(5002)
		}
		w.I16(-120)
		w.I16(215)
		w.U8(1)
		w.U8(0)
		w.U8(0)
		q, err := ReadSkillCast(NewReader(w.Packet()))
		if err != nil || q.SkillID != 35111002 || q.Level != 20 || !q.HasPosition || q.X != -120 || q.Y != 215 {
			t.Fatalf("tesla coil count=%d: got %+v, err=%v", coils, q, err)
		}
	}
	// Invalid Tesla Coil count (>2) with 8+ bytes must be rejected.
	wBad := NewRawWriter()
	wBad.U32(99)
	wBad.I32(35111002)
	wBad.U8(20)
	wBad.U8(3)
	wBad.I16(0)
	wBad.I16(0)
	wBad.U8(1)
	wBad.U8(0)
	wBad.U8(0)
	if _, err := ReadSkillCast(NewReader(wBad.Packet())); err == nil {
		t.Fatal("expected invalid tesla coil count=3 to be rejected")
	}

	// 33101008 Wild Hunter Mine: 8-byte tail (x, y, facing, bAttack, 0, state).
	wMine := NewRawWriter()
	wMine.U32(100)
	wMine.I32(33101008)
	wMine.U8(10)
	wMine.I16(44)
	wMine.I16(-12)
	wMine.U8(0)
	wMine.U8(1)
	wMine.U8(0)
	wMine.U8(1)
	qMine, err := ReadSkillCast(NewReader(wMine.Packet()))
	if err != nil || qMine.SkillID != 33101008 || !qMine.HasPosition || qMine.X != 44 || qMine.Y != -12 {
		t.Fatalf("wild hunter mine: got %+v, err=%v", qMine, err)
	}

	// 30001062 Call of the Hunter: 9-byte tail (mobTemplate, x, y, facing).
	wCall := NewRawWriter()
	wCall.U32(101)
	wCall.I32(30001062)
	wCall.U8(1)
	wCall.I32(100100)
	wCall.I16(88)
	wCall.I16(215)
	wCall.U8(1)
	qCall, err := ReadSkillCast(NewReader(wCall.Packet()))
	if err != nil || qCall.SkillID != 30001062 || !qCall.HasPosition || qCall.X != 88 || qCall.Y != 215 {
		t.Fatalf("call of the hunter: got %+v, err=%v", qCall, err)
	}
}

func TestSkillCancelNativeModeByte(t *testing.T) {
	w := NewRawWriter()
	w.I32(5221006)
	w.U8(0)
	if id, err := ReadSkillCancel(NewReader(w.Packet())); err != nil || id != 5221006 {
		t.Fatalf("native cancel %d %v", id, err)
	}
	w = NewRawWriter()
	w.I32(5221006)
	w.U16(0)
	if _, err := ReadSkillCancel(NewReader(w.Packet())); err == nil {
		t.Fatal("unexplained trailing bytes accepted")
	}
	for id, ok := range map[int32]bool{-2002000: true, -1: false, -3000000: false} {
		w = NewRawWriter()
		w.I32(id)
		w.U8(0)
		got, err := ReadSkillCancel(NewReader(w.Packet()))
		if ok != (err == nil) || ok && got != int(id) {
			t.Fatalf("item cancel %d: %d %v", id, got, err)
		}
	}
}
