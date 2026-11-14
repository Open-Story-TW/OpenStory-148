package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestUseItemWireContract(t *testing.T) {
	// Legacy USE_ITEM: tick:u32, USE slot:i16, template:i32.
	body, _ := hex.DecodeString("ffffffff010080841e00")
	req, err := ReadUseItem(NewReader(body))
	if err != nil || req.ClientTick != ^uint32(0) || req.Slot != 1 || req.TemplateID != 2000000 {
		t.Fatalf("request=%+v error=%v", req, err)
	}
	for n := 0; n < len(body); n++ {
		if _, err := ReadUseItem(NewReader(body[:n])); err == nil {
			t.Fatalf("accepted truncated request of %d bytes", n)
		}
	}
	for _, malformed := range []string{"ffffffff000080841e00", "ffffffffffff80841e00", "ffffffff810080841e00", "ffffffff0100ffffffff", "ffffffff010080841e0000"} {
		data, _ := hex.DecodeString(malformed)
		if _, err := ReadUseItem(NewReader(data)); err == nil {
			t.Fatalf("accepted malformed request %s", malformed)
		}
	}
}

func TestUseItemResponseBytes(t *testing.T) {
	// Byte layouts from CWvsContext.InventoryPacket.modifyInventory and
	// updatePlayerStats (HP mask 0x400; int32 HP, two trailing zero bytes).
	for _, tc := range []struct {
		name string
		got  []byte
		hex  string
	}{
		{"quantity", UpdateUseSlot(1, 4), "2a00000100010201000400"},
		{"remove", UpdateUseSlot(1, 0), "2a0000010003020100"},
		{"hp", UpdateHP(55), "2c00010004000000000000370000000000"},
	} {
		want, _ := hex.DecodeString(tc.hex)
		if !bytes.Equal(tc.got, want) {
			t.Fatalf("%s: got %x want %x", tc.name, tc.got, want)
		}
	}
	if UpdateUseSlot(0, 1) != nil || UpdateUseSlot(1, -1) != nil || UpdateUseSlot(1, 30001) != nil || UpdateHP(-1) != nil {
		t.Fatal("accepted out-of-range response")
	}
}
