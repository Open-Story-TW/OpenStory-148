// USE_ITEM and inventory/stat updates derived from Legacy InventoryHandler and
// CWvsContext.InventoryPacket (AGPLv3). Only ordinary USE slots are supported.
package protocol

import "maple-go-server/internal/domain"

const (
	RecvUseItem         uint16 = 0x7c
	SendModifyInventory uint16 = 0x2a
)

func ReadUseItem(r *Reader) (domain.UseItemRequest, error) {
	req := domain.UseItemRequest{ClientTick: r.U32(), Slot: int(r.I16()), TemplateID: int(r.I32())}
	if r.Err() != nil || r.Remaining() != 0 || req.Slot < 1 || req.Slot > 128 || req.TemplateID <= 0 {
		return domain.UseItemRequest{}, ErrMalformed
	}
	return req, nil
}

// UpdateUseSlot changes a quantity or removes the exhausted stack. The extra
// zero before the operation is present in Legacy's modifyInventory header.
func UpdateUseSlot(slot, quantity int) []byte {
	if slot < 1 || slot > 128 || quantity < 0 || quantity > 30000 {
		return nil
	}
	w := NewWriter(SendModifyInventory)
	w.U8(0) // updateTick
	w.U8(1) // operation count
	w.U8(0)
	if quantity == 0 {
		w.U8(3) // REMOVE
	} else {
		w.U8(1) // UPDATE
	}
	w.U8(2) // USE
	w.I16(int16(slot))
	if quantity != 0 {
		w.I16(int16(quantity))
	}
	return w.Packet()
}

func UpdateHP(hp int) []byte {
	if hp < 0 || int64(hp) > 2147483647 {
		return nil
	}
	w := NewWriter(SendUpdateStats)
	w.U8(1) // unlock native item action
	w.I64(0x400)
	w.I32(int32(hp))
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

func UpdateHPMP(hp, mp int) []byte {
	if hp < 0 || mp < 0 || int64(hp) > 2147483647 || int64(mp) > 2147483647 {
		return nil
	}
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x1400)
	w.I32(int32(hp))
	w.I32(int32(mp))
	w.U8(0)
	w.U8(0)
	return w.Packet()
}
