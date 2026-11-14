package protocol

import (
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestStoragePacketContracts(t *testing.T) {
	// The golden bytes are the stock four-byte balance layout.
	defer domain.SetLongMoneyForTest(false)()
	storage := domain.Storage{Capacity: 48, Mesos: 100, Items: []domain.StoredItem{}}
	r := NewReader(OpenStorage(1022005, storage))
	if r.U16() != SendStorage || r.U8() != 0x16 || r.I32() != 1022005 || r.U8() != 48 || r.U16() != 0x7e || r.U16() != 0 || r.I32() != 0 || r.I32() != 100 || r.U16() != 0 || r.U8() != 0 || r.U16() != 0 || r.Remaining() != 0 {
		t.Fatal("storage open layout")
	}
	r = NewReader(StorageMesos(storage))
	if r.U16() != SendStorage || r.U8() != 0x13 || r.U8() != 48 || r.U16() != 2 || r.U16() != 0 || r.I32() != 0 || r.I32() != 100 || r.Remaining() != 0 {
		t.Fatal("storage mesos layout")
	}
	shop, err := content.NPCShop(2)
	if err != nil {
		t.Fatal(err)
	}
	r = NewReader(OpenNPCShop(shop))
	if r.U16() != SendNPCShop || r.I32() != 0 || r.I32() != 11100 {
		t.Fatal("shop open header")
	}
	_ = r.I32()
	if r.U8() != 0 || int(r.U16()) != len(shop.Items) || r.I32() != 2000000 || r.I32() != 5 {
		t.Fatal("catalog shop first item/price")
	}
}

func TestNativeStorageGenericRejectionHasNoPayload(t *testing.T) {
	if got := StorageRejected(); len(got) != 3 || got[0] != 0x46 || got[1] != 0x02 || got[2] != 14 {
		t.Fatalf("native generic rejection must be0x223/action14 only: %x", got)
	}
}

// Follow the actual native decoder0x6F366B: a mask-selected type has its own
// U8 count, and the withdraw index resets at zero for that type. The fixed
// record sizes below come from independently audited v145 native item layouts,
// not a call back into the encoder under test. Fixture owners are empty.
func decodeNativeStorageGroups(t *testing.T, packet []byte, action byte) map[int][]int {
	t.Helper()
	r := NewReader(packet)
	if r.U16() != 0x246 || r.U8() != action {
		t.Fatal("native storage header")
	}
	if action == 22 && r.I32() != 1022005 {
		t.Fatal("native storage npc")
	}
	if r.U8() != 48 {
		t.Fatal("native storage capacity")
	}
	mask := uint64(r.I64())
	if mask&2 != 0 && r.I32() != 100 {
		t.Fatal("native stored mesos")
	}
	groups := map[int][]int{}
	for invType := 1; invType <= 5; invType++ {
		if mask&(2<<invType) == 0 {
			continue
		}
		count := int(r.U8())
		for index := 0; index < count; index++ {
			kind, templateID := r.U8(), int(r.I32())
			if templateID/1000000 != invType {
				t.Fatalf("type%d index%d contains template%d", invType, index, templateID)
			}
			size := 24 // normal, empty-owner GW_ItemSlotBundle
			if kind == 1 {
				size = 112 // v148 adds three u16 to the equipment record.
			} else if kind != 2 {
				t.Fatalf("native item kind%d", kind)
			}
			r.Skip(size - 5)
			groups[invType] = append(groups[invType], templateID)
		}
	}
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("native storage decoder not exactly consumed: %v remaining%d", r.Err(), r.Remaining())
	}
	return groups
}

func TestNativeMixedStorageOpenAndArrangeKeepPerTypeWithdrawalIndexes(t *testing.T) {
	// The golden bytes are the stock four-byte balance layout.
	defer domain.SetLongMoneyForTest(false)()
	storage := domain.Storage{Capacity: 48, Mesos: 100}
	// Interleave the five bags deliberately; global indexes0/1 are not the
	// type-local indexes0/1 which native withdraw sends to handleStorage.
	for _, id := range []int{2000001, 1002002, 4000001, 5040001, 3010000, 4000002, 3010001, 2000002, 5040000, 1002001} {
		item := domain.StoredItem{ID: int64(len(storage.Items) + 1), Item: domain.InventoryItem{InvType: id / 1000000, TemplateID: id, Quantity: 1}}
		if id/1000000 == 1 {
			item.Equipment = &domain.Equipment{ID: item.ID, ItemID: id}
		}
		storage.Items = append(storage.Items, item)
	}
	for _, test := range []struct {
		name   string
		packet []byte
		action byte
	}{{"open", OpenStorage(1022005, storage), 22}, {"arrange", StorageArrange(storage), 15}} {
		t.Run(test.name, func(t *testing.T) {
			groups := decodeNativeStorageGroups(t, test.packet, test.action)
			for invType := 1; invType <= 5; invType++ {
				var authoritative []domain.StoredItem
				for _, item := range storage.Items {
					if item.Item.InvType == invType {
						authoritative = append(authoritative, item)
					}
				}
				if len(groups[invType]) != 2 {
					t.Fatalf("type%d native count=%d", invType, len(groups[invType]))
				}
				for index := 0; index < 2; index++ {
					if groups[invType][index] != authoritative[index].Item.TemplateID {
						t.Fatalf("type%d withdraw index%d maps to%d, want storageID%d/template%d", invType, index, groups[invType][index], authoritative[index].ID, authoritative[index].Item.TemplateID)
					}
				}
			}
		})
	}
}
