package protocol

import (
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
)

func TestSpawnRemoveMovePlayerOpcodes(t *testing.T) {
	c := domain.Character{
		ID: 42, Name: "AoI", Level: 1, Job: 0, Gender: 0, Skin: 0, Face: 20100, Hair: 30000,
		HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, STR: 4, DEX: 4, INT: 4, LUK: 4, X: 100, Y: 200, Revision: 1,
		Equipment: []domain.Equipment{{ID: 1, ItemID: 1040010, Slot: -5}},
	}
	spawn := SpawnPlayer(c, 4)
	if spawn == nil || binary.LittleEndian.Uint16(spawn[:2]) != SendSpawnPlayer {
		t.Fatalf("spawn opcode")
	}
	if binary.LittleEndian.Uint32(spawn[2:6]) != 42 {
		t.Fatalf("spawn id")
	}
	remove := RemovePlayer(42)
	if binary.LittleEndian.Uint16(remove[:2]) != SendRemovePlayer || binary.LittleEndian.Uint32(remove[2:6]) != 42 {
		t.Fatalf("remove packet")
	}
	moveBody := EncodeAbsolutePlayerMove(0, 0, 10, 20)
	r := NewReader(moveBody)
	r.U16()
	parsed, err := ParsePlayerMove(r)
	if err != nil {
		t.Fatal(err)
	}
	out := MovePlayer(42, parsed.StartX, parsed.StartY, parsed.Fragments)
	if binary.LittleEndian.Uint16(out[:2]) != SendMovePlayer {
		t.Fatalf("move opcode")
	}
	if binary.LittleEndian.Uint32(out[2:6]) != 42 {
		t.Fatalf("move id")
	}
}

func TestDefaultForeignStatsMatchLegacyRecordBoundaries(t *testing.T) {
	w := NewRawWriter()
	writeEmptyForeignBuffs(w)
	data := w.Packet()
	// Java PacketHelper.addForeignBuffState, no active buffs: mask32 + flags3
	// + ENERGY/DASH/DASH (15 each) + MOUNT13 + SPEED20 + HOMING21 + DEFAULT15/13.
	if len(data) != 162 {
		t.Fatalf("foreign temporary stats length=%d, want162", len(data))
	}
	if binary.LittleEndian.Uint32(data) != 0xff000000 {
		t.Fatal("wrong default temporary-stat mask")
	}
	for _, flag := range data[32:35] {
		if flag != 0 {
			t.Fatal("nonzero foreign stat header flag")
		}
	}
	for _, start := range []int{35, 50, 65, 80, 93, 113, 134, 149} {
		if binary.LittleEndian.Uint64(data[start:start+8]) != 0 || data[start+8] != 1 || binary.LittleEndian.Uint32(data[start+9:start+13]) != 1 {
			t.Fatalf("foreign record misaligned at offset%d", start)
		}
	}
}
