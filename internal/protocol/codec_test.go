package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestReaderBounds(t *testing.T) {
	for _, data := range [][]byte{nil, {1}, {255, 255}, {3, 0, 'a'}} {
		r := NewReader(data)
		_ = r.String()
		if !errors.Is(r.Err(), ErrMalformed) {
			t.Fatalf("accepted truncated input %x", data)
		}
	}
	r := NewReader([]byte{1})
	if r.Bytes(-1) != nil || r.Err() == nil {
		t.Fatal("negative length accepted")
	}
}
func TestBig5Codec(t *testing.T) {
	w := NewRawWriter()
	w.String("楓之谷")
	w.I32(-2147483648)
	w.I64(0x102030405060708)
	if w.Err() != nil {
		t.Fatal(w.Err())
	}
	wire := w.Packet()
	if bytes.Contains(wire, []byte("楓")) {
		t.Fatal("wire unexpectedly UTF-8")
	}
	r := NewReader(wire)
	if got := r.String(); got != "楓之谷" {
		t.Fatalf("Big5 roundtrip %q", got)
	}
	if r.I32() != -2147483648 || r.I64() != 0x102030405060708 || r.Err() != nil {
		t.Fatal("numeric roundtrip failed")
	}
	w = NewRawWriter()
	w.FixedString("楓楓", 3)
	if w.Err() == nil || w.Packet() != nil {
		t.Fatal("truncated Big5 character accepted")
	}
}
func TestRejectedCreationHasNoCharacterPayload(t *testing.T) {
	p := CreateCharacterResult(emptyCharacter(), false)
	if !bytes.Equal(p, []byte{6, 0, 1}) {
		t.Fatalf("bad rejectpacket %x", p)
	}
}
