package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

func TestInventoryMoveWire(t *testing.T) {
	data, _ := hex.DecodeString("0100000001f5ff0100ffff")
	r, err := ReadInventoryMove(NewReader(data))
	if err != nil || r.ClientTick != 1 || r.InvType != 1 || r.Source != -11 || r.Destination != 1 || r.Quantity != -1 {
		t.Fatalf("%+v %v", r, err)
	}
	for n := 0; n < len(data); n++ {
		if _, err := ReadInventoryMove(NewReader(data[:n])); err == nil {
			t.Fatalf("accepted truncated request %d", n)
		}
	}
	for _, tc := range []struct {
		change domain.InventoryChange
		hex    string
	}{
		{domain.InventoryChange{Mode: 2, InvType: 1, Slot: -11, Destination: 1}, "2a000101000201f5ff010001"},
		{domain.InventoryChange{Mode: 2, InvType: 1, Slot: 1, Destination: -11}, "2a0001010002010100f5ff02"},
		{domain.InventoryChange{Mode: 2, InvType: 2, Slot: 1, Destination: 2}, "2a00010100020201000200"},
	} {
		want, _ := hex.DecodeString(tc.hex)
		if got := ModifyInventory([]domain.InventoryChange{tc.change}); !bytes.Equal(got, want) {
			t.Fatalf("got%x want%x", got, want)
		}
	}
}

func TestInventoryDropAndBagEncoding(t *testing.T) {
	drop := domain.ItemDrop{ObjectID: 1000000000, OwnerID: 1, TemplateID: 2000000, X: -192, Y: 210}
	p := DropInventoryItem(drop)
	if len(p) != 35 || p[7] != 0 {
		t.Fatalf("drop=%x", p)
	}
	r := NewReader(p)
	r.Skip(17)
	if r.I16() != -192 || r.I16() != 210 || r.I32() != 0 || r.I64() != maxTime || r.U16() != 0 || r.Err() != nil {
		t.Fatalf("drop packet shape=%x", p)
	}
	w := NewRawWriter()
	AddEquipmentInventory(w, []domain.Equipment{{ID: 1, ItemID: 1322005, Slot: -11}, {ID: 2, ItemID: 1072005, Slot: 3}})
	r = NewReader(w.Packet())
	if r.U16() != 3 || r.U8() != 1 || r.I32() != 1072005 {
		t.Fatal("positive equipment bag not encoded")
	}
	if got := w.Packet(); !bytes.Equal(got[len(got)-2:], []byte{0, 0}) {
		t.Fatal("equipment bag missing terminator")
	}
}
