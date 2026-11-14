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

// Layouts follow the native emitter 0x89FF18 (docs/baseline/skill-effect-native.json).
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
