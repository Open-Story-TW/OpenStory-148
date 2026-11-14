package protocol

import "maple-go-server/internal/domain"

const RecvStorage uint16 = 0x6d
const SendStorage uint16 = 0x246

func OpenStorage(npcID int, storage domain.Storage) []byte {
	w := NewWriter(SendStorage)
	w.U8(0x16)
	w.I32(int32(npcID))
	w.U8(byte(storage.Capacity))
	w.U16(0x7e)
	w.U16(0)
	w.I32(0)
	writeMoney(w, storage.Mesos)
	// Native0x6F366B reads a separate count and type-local index sequence
	// for every selected inventory type. Mixed items cannot share one group.
	for invType := 1; invType <= 5; invType++ {
		writeStorageItems(w, storage.Items, invType)
	}
	return w.Packet()
}

func StorageItems(storage domain.Storage, invType int, deposit bool) []byte {
	w := NewWriter(SendStorage)
	if deposit {
		w.U8(0x0d)
	} else {
		w.U8(9)
	}
	w.U8(byte(storage.Capacity))
	w.U16(uint16(2 << invType))
	w.U16(0)
	w.I32(0)
	writeStorageItems(w, storage.Items, invType)
	return w.Packet()
}
func StorageArrange(storage domain.Storage) []byte {
	w := NewWriter(SendStorage)
	w.U8(0x0f)
	w.U8(byte(storage.Capacity))
	w.U8(0x7c)
	w.Zero(7)
	for invType := 1; invType <= 5; invType++ {
		writeStorageItems(w, storage.Items, invType)
	}
	return w.Packet()
}
func StorageMesos(storage domain.Storage) []byte {
	w := NewWriter(SendStorage)
	w.U8(0x13)
	w.U8(byte(storage.Capacity))
	w.U16(2)
	w.U16(0)
	w.I32(0)
	writeMoney(w, storage.Mesos)
	return w.Packet()
}
func StorageFull() []byte { w := NewWriter(SendStorage); w.U8(0x11); return w.Packet() }

// Native0x6F3B2A clears the storage window's busy flag before dispatching
// action14 to its generic StringPool1005 notice. It consumes no extra payload.
// This rejects a request without falsely reporting capacity/funds or success.
func StorageRejected() []byte { w := NewWriter(SendStorage); w.U8(14); return w.Packet() }

func writeStorageItems(w *Writer, items []domain.StoredItem, invType int) {
	count := 0
	for _, item := range items {
		if invType == 0 || item.Item.InvType == invType {
			count++
		}
	}
	w.U8(byte(count))
	for _, item := range items {
		if invType != 0 && item.Item.InvType != invType {
			continue
		}
		if item.Equipment != nil {
			addEquipment(w, *item.Equipment)
		} else {
			addBundleItem(w, item.Item)
		}
	}
}
