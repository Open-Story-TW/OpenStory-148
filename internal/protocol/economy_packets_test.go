package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
)

func TestExpiredDropAnimationHasNoPickupCharacter(t *testing.T) {
	// Native expiration: opcode, mode=0, object ID; no pickup actor.
	if got, want := RemoveItemExpired(0x12345678), []byte{0xe3, 0x01, 0, 0x78, 0x56, 0x34, 0x12}; !bytes.Equal(got, want) {
		t.Fatalf("expiry packet: %x != %x", got, want)
	}
	if RemoveItemExpired(0) != nil {
		t.Fatal("invalid object ID emitted")
	}
}

func TestNativePickupRemovalHasExactActorPayload(t *testing.T) {
	got := RemoveItemPickup(0x12345678, 101)
	want := []byte{0xe3, 0x01, 2, 0x78, 0x56, 0x34, 0x12, 101, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("native pickup removal must have body9/packet11: %x != %x", got, want)
	}
	if RemoveItemPickup(0, 101) != nil || RemoveItemPickup(7, 0) != nil {
		t.Fatal("invalid pickup identity emitted")
	}
}

func TestNativeMesoDropIncludesClientTick(t *testing.T) {
	packet := []byte{0x78, 0x56, 0x34, 0x12, 100, 0, 0, 0}
	request, err := ReadMesoDrop(NewReader(packet))
	if err != nil || request.Tick != 0x12345678 || request.Amount != 100 {
		t.Fatalf("native meso drop: %+v %v", request, err)
	}
	for _, malformed := range [][]byte{packet[:4], append(append([]byte(nil), packet...), 0)} {
		if _, err := ReadMesoDrop(NewReader(malformed)); err == nil {
			t.Fatal("accepted legacy/trailing meso payload")
		}
	}
}

func TestUpdateMesosAndDropPackets(t *testing.T) {
	stats := UpdateMesos(1000, true)
	if stats == nil || binary.LittleEndian.Uint16(stats[:2]) != SendUpdateStats {
		t.Fatal("update mesos opcode")
	}
	if binary.LittleEndian.Uint64(stats[3:11]) != uint64(statMeso) {
		t.Fatalf("mask=%x", binary.LittleEndian.Uint64(stats[3:11]))
	}
	drop := DropMesoOnMap(7, 100, 42, 10, 20, true)
	if drop == nil || binary.LittleEndian.Uint16(drop[:2]) != SendDropItem {
		t.Fatal("drop opcode")
	}
	if drop[2] != 2 || drop[7] != 1 {
		t.Fatalf("drop header mod/type")
	}
	remove := RemoveItemPickup(7, 42)
	if remove == nil || binary.LittleEndian.Uint16(remove[:2]) != SendRemoveItem || remove[2] != 2 {
		t.Fatal("remove packet")
	}
}

func TestInventoryBagEncode(t *testing.T) {
	w := NewRawWriter()
	addInventoryBags(w, []domain.InventoryItem{{ID: 9, TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 5}})
	p := w.Packet()
	if len(p) < 5 || p[0] != 1 {
		t.Fatalf("use slot header=%v", p)
	}
	if p[1] != 2 {
		t.Fatalf("bundle type=%d", p[1])
	}
	if binary.LittleEndian.Uint32(p[2:6]) != 2000000 {
		t.Fatalf("template=%d", binary.LittleEndian.Uint32(p[2:6]))
	}
	if p[len(p)-3] != 0 || p[len(p)-2] != 0 || p[len(p)-1] != 0 {
		t.Fatalf("bag terminators=%v", p[len(p)-3:])
	}
}
