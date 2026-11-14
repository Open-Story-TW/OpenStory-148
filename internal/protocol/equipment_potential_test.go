package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestEquipmentPotentialNativeTailAndLegacySentinels(t *testing.T) {
	for _, cashID := range []int64{0, 123456789} {
		plain := domain.Equipment{ItemID: 1322005, CashID: cashID}
		w := NewRawWriter()
		addEquipment(w, plain)
		legacy := w.Packet()
		suffix := 26 // v148 three u16 + ordinary uniqueID8 + recordTime8 + tail4
		if cashID != 0 {
			suffix = 18
		}
		offset := len(legacy) - suffix - 14
		oldTail, _ := hex.DecodeString("0000000000000000ffffffff0000")
		if !bytes.Equal(legacy[offset:offset+14], oldTail) {
			t.Fatalf("legacy state/short sentinels changed: %x", legacy[offset:offset+14])
		}
		plain.Potential = domain.EquipmentPotential{RawState: 0x14, Options: [5]uint16{0xffef, 2, 30003, 0xffff, 5}, Flags: 0x1f00}
		plain.Enhance = 254
		w = NewRawWriter()
		addEquipment(w, plain)
		got := w.Packet()
		want, _ := hex.DecodeString("14feefff02003375ffff0500001f")
		if len(got) != len(legacy) || !bytes.Equal(got[:offset], legacy[:offset]) || !bytes.Equal(got[offset:offset+14], want) || !bytes.Equal(got[offset+14:], legacy[offset+14:]) {
			t.Fatalf("native tail changed record shape cashID=%d got=%x legacy=%x", cashID, got, legacy)
		}
		plain.Potential = domain.EquipmentPotential{}
		w = NewRawWriter()
		addEquipment(w, plain)
		got = w.Packet()
		if got[offset+1] != 254 || !bytes.Equal(got[offset+2:offset+14], oldTail[2:]) {
			t.Fatal("independent enhance altered potential default shorts")
		}
		// A stored 0xffff must not reach the v148 socket state.
		plain.Potential = domain.EquipmentPotential{RawState: 0x14, Flags: 0xffff}
		w = NewRawWriter()
		addEquipment(w, plain)
		if got = w.Packet(); !bytes.Equal(got[offset+12:offset+14], []byte{0, 0}) {
			t.Fatalf("socket state sent as %x", got[offset+12:offset+14])
		}
		plain.Potential = domain.EquipmentPotential{}
		plain.Potential.Present = true
		plain.Enhance = 0
		w = NewRawWriter()
		addEquipment(w, plain)
		if got = w.Packet(); len(got) != len(legacy) || !bytes.Equal(got[offset:offset+14], make([]byte, 14)) {
			t.Fatalf("explicit native zero tail changed into legacy sentinels: %x", got)
		}
	}
}

func TestEquipmentPotentialFullRecordContainerCodecs(t *testing.T) {
	e := domain.Equipment{ID: 101, ItemID: 1322005, Slot: 1, Enhance: 254,
		Potential: domain.EquipmentPotential{Present: true, RawState: 0xf1, Options: [5]uint16{0xffef, 1234, 2345, 3456, 4567}, Flags: 0xa539}}
	tail, _ := hex.DecodeString("f1feefffd2042909800dd71139a5")
	cashEquip := e
	cashEquip.CashID = 9012345
	item := domain.CashItem{ID: cashEquip.CashID, InvType: 1, TemplateID: e.ItemID, Quantity: 1, Slot: 1, Equipment: &cashEquip}
	c := starterCharacter()
	c.Equipment = []domain.Equipment{e}
	bag := NewRawWriter()
	AddEquipmentInventory(bag, c.Equipment)
	worn := c
	worn.Equipment = []domain.Equipment{e}
	worn.Equipment[0].Slot = -11
	for name, packet := range map[string][]byte{
		"bag":         bag.Packet(),
		"field_bag":   setFieldAt(c, 1, time.Unix(0, 0), [12]byte{}),
		"field_worn":  setFieldAt(worn, 1, time.Unix(0, 0), [12]byte{}),
		"modify":      ModifyInventory([]domain.InventoryChange{{Mode: 0, InvType: 1, Slot: 1, Equipment: &e}}),
		"trade":       TradeItem(0, domain.TradeItem{TradeSlot: 1, Equipment: &e}),
		"storage":     OpenStorage(2006, domain.Storage{Capacity: 4, Items: []domain.StoredItem{{Item: domain.InventoryItem{InvType: 1}, Equipment: &e}}}),
		"cash_locker": CashLocker([]domain.CashItem{item}),
		"cash_return": CashFromLocker(item, domain.Character{Equipment: []domain.Equipment{cashEquip}}),
	} {
		if bytes.Count(packet, tail) != 1 {
			t.Fatalf("%s lost/duplicated native metadata tail: %x", name, packet)
		}
	}
}
