package protocol

import (
	"bytes"
	"testing"
)

func TestDragonPackets(t *testing.T) {
	spawn := SpawnDragon(7, -100, 200, 4, 2210)
	want := []byte{0x29, 0x01, 7, 0, 0, 0, 0x9c, 0xff, 0xff, 0xff, 0xc8, 0, 0, 0, 4, 0, 0, 0xa2, 0x08}
	if !bytes.Equal(spawn, want) {
		t.Fatalf("spawn % x", spawn)
	}
	// start (10,20), 4 zero bytes, one absolute command to (30,40).
	frag := []byte{1, 0, 30, 0, 40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0}
	body := append([]byte{10, 0, 20, 0, 0, 0, 0, 0}, frag...)
	mv, err := ReadDragonMove(NewReader(body))
	if err != nil || mv.StartX != 10 || mv.StartY != 20 || mv.X != 30 || mv.Y != 40 || !mv.Changed || !bytes.Equal(mv.Fragments, frag) {
		t.Fatalf("move %+v %v", mv, err)
	}
	out := MoveDragon(7, mv.StartX, mv.StartY, mv.Fragments)
	if !bytes.Equal(out[:14], []byte{0x2a, 0x01, 7, 0, 0, 0, 10, 0, 20, 0, 0, 0, 0, 0}) || !bytes.Equal(out[14:], frag) {
		t.Fatalf("move out % x", out)
	}
	if !HasDragon(2200) || !HasDragon(2218) || HasDragon(2001) || HasDragon(2300) {
		t.Fatal("dragon jobs")
	}
}
