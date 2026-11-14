package protocol

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestInitialKeymapContainsRealJavaBindingsWithoutLocalCacheDependency(t *testing.T) {
	p := DefaultKeymap()
	if len(p) != 448 {
		t.Fatalf("complete native 89-record packet length %d", len(p))
	}
	r := NewReader(p)
	if r.U16() != SendKeymap || r.U8() != 0 {
		t.Fatal("default delegated to local cached keymap")
	}
	var bindings domain.Keymap
	count := 0
	for i := range bindings {
		bindings[i] = domain.KeyBinding{Type: r.U8(), Action: r.I32()}
		if bindings[i].Type != 0 {
			count++
		}
	}
	if count != 44 || bindings[56] != (domain.KeyBinding{Type: 5, Action: 53}) || bindings[29] != (domain.KeyBinding{Type: 5, Action: 52}) || r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("Java key assignments not retained: %d %+v", count, bindings)
	}
}

func TestNativeKeymapChangesValidateCountAndUniqueSlots(t *testing.T) {
	w := NewRawWriter()
	w.I32(0)
	w.U32(2)
	w.I32(56)
	w.U8(0)
	w.I32(0)
	w.I32(57)
	w.U8(5)
	w.I32(53)
	q, err := ReadKeymapChanges(NewReader(w.Packet()))
	if err != nil || len(q) != 2 || q[1].Binding.Action != 53 {
		t.Fatalf("valid changes %+v %v", q, err)
	}
	p := w.Packet()
	for i := 0; i < len(p); i++ {
		if _, err = ReadKeymapChanges(NewReader(p[:i])); err == nil {
			t.Fatalf("truncated keymap accepted at %d", i)
		}
	}
	if _, err = ReadKeymapChanges(NewReader(append(p, 0))); err == nil {
		t.Fatal("trailing key bytes accepted")
	}
	bad := append([]byte(nil), p...)
	copy(bad[17:21], bad[8:12])
	if _, err = ReadKeymapChanges(NewReader(bad)); err == nil {
		t.Fatal("duplicate slot accepted")
	}
	bad = append([]byte(nil), p...)
	bad[8] = 89
	if _, err = ReadKeymapChanges(NewReader(bad)); err == nil {
		t.Fatal("outside native map accepted")
	}
}
