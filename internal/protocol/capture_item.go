package protocol

const RecvCaptureItem uint16 = 0x8a

type CaptureItemRequest struct {
	Slot, ItemID int
	ObjectID     int32
}

// TWMS v148 CWvsContext captures with tick, source slot, item and target.
func ReadCaptureItem(r *Reader) (CaptureItemRequest, error) {
	r.U32()
	p := CaptureItemRequest{Slot: int(r.I16()), ItemID: int(r.I32()), ObjectID: r.I32()}
	if r.Err() != nil || r.Remaining() != 0 || p.Slot < 1 || p.ItemID/10000 != 227 || p.ObjectID <= 0 {
		return CaptureItemRequest{}, ErrMalformed
	}
	return p, nil
}

func CaptureMonsterResult(objectID int32, itemID int, caught bool) []byte {
	w := NewWriter(0x1bc)
	w.I32(objectID)
	w.I32(int32(itemID))
	w.Bool(caught)
	return w.Packet()
}
