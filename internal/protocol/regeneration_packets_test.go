package protocol

import (
	"bytes"
	"testing"
)

func nativeRegenerationFixture(op uint16, hp, mp int, mode byte) []byte {
	w := NewRawWriter()
	w.U32(123)
	w.U32(0x1400)
	w.U32(0)
	w.I16(int16(hp))
	w.I16(int16(mp))
	if op == RecvRegeneration {
		w.U8(mode)
	}
	return w.Packet()
}
func TestNativeRegenerationExact91And92(t *testing.T) {
	for _, op := range []uint16{RecvRegeneration, RecvPotentialRegeneration} {
		for _, mode := range []byte{0, 2} {
			if op == RecvPotentialRegeneration && mode != 0 {
				continue
			}
			body := nativeRegenerationFixture(op, 2, 3, mode)
			q, err := ReadRegenerationOpcode(op, NewReader(body))
			if err != nil || q.Tick != 123 || q.HP != 2 || q.MP != 3 || q.Mode != mode || q.Opcode != op || !bytes.Equal(q.Wire, body) {
				t.Fatal(q, err)
			}
			body[0] = 9
			if q.Wire[0] != 123 {
				t.Fatal("wire aliased receive buffer")
			}
			for _, change := range []func([]byte) []byte{func(v []byte) []byte { return v[:len(v)-1] }, func(v []byte) []byte { return append(v, 0) }, func(v []byte) []byte { v[5] = 0; return v }, func(v []byte) []byte { v[8] = 1; return v }, func(v []byte) []byte { v[12] = 255; v[13] = 255; return v }} {
				raw := nativeRegenerationFixture(op, 2, 3, mode)
				if _, err = ReadRegenerationOpcode(op, NewReader(change(raw))); err == nil {
					t.Fatal("bad native regen accepted", op)
				}
			}
		}
	}
	for _, n := range []int{8, 12, 16} {
		if _, err := ReadRegeneration(NewReader(make([]byte, n))); err == nil {
			t.Fatal("guessed compatibility length accepted", n)
		}
	}
	for _, mode := range []byte{1, 3, 255} {
		if _, err := ReadRegeneration(NewReader(nativeRegenerationFixture(RecvRegeneration, 1, 0, mode))); err == nil {
			t.Fatal("unproven mode", mode)
		}
	}
	if _, err := ReadRegenerationOpcode(RecvPotentialRegeneration, NewReader(nativeRegenerationFixture(RecvRegeneration, 1, 0, 0))); err == nil {
		t.Fatal("91 mode interpreted as92")
	}
}
