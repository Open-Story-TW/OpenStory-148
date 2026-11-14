// Meso drop/pickup and meso stat updates derived from Legacy CField/CWvsContext (AGPLv3).
package protocol

import "maple-go-server/internal/domain"

const (
	RecvMesoDrop   uint16 = 0xa6
	RecvItemPickup uint16 = 0x17a
	SendDropItem   uint16 = 0x1e1
	SendRemoveItem uint16 = 0x1e3
	statMeso       int64  = 0x40000
)

type MesoDrop struct {
	Tick   uint32
	Amount int32
}

// Native CWvsContext 0x934EC1 writes tickU32 then amountI32 at 0x934FB8/0x934FC3.
func ReadMesoDrop(r *Reader) (MesoDrop, error) {
	request := MesoDrop{Tick: r.U32(), Amount: r.I32()}
	if r.Err() != nil || r.Remaining() != 0 {
		return request, ErrMalformed
	}
	return request, nil
}

// UpdateMesos writes UPDATE_STATS with the MESO mask (0x40000).
func UpdateMesos(mesos int64, enableActions bool) []byte {
	if mesos < 0 || mesos > domain.MaxMoney() {
		return nil
	}
	w := NewWriter(SendUpdateStats)
	if enableActions {
		w.U8(1)
	} else {
		w.U8(0)
	}
	w.I64(statMeso)
	writeMoney(w, mesos)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

// DropMesoOnMap spawns a meso pile with mod=2 (no throw animation).
func DropMesoOnMap(objectID, amount, ownerID int32, x, y int16, playerDrop bool) []byte {
	if objectID <= 0 || amount <= 0 || ownerID <= 0 {
		return nil
	}
	w := NewWriter(SendDropItem)
	w.U8(2) // no animation
	w.I32(objectID)
	w.U8(1) // meso
	w.I32(amount)
	w.I32(ownerID)
	w.U8(2) // FFA
	w.I16(x)
	w.I16(y)
	w.I32(0)
	if playerDrop {
		w.U16(0)
	} else {
		w.U16(1)
	}
	return w.Packet()
}

// RemoveItemPickup removes a map object after a successful pickup (animation 2).
func RemoveItemPickup(objectID, characterID int32) []byte {
	if objectID <= 0 || characterID <= 0 {
		return nil
	}
	w := NewWriter(SendRemoveItem)
	w.U8(2)
	w.I32(objectID)
	w.I32(characterID)
	// CDropPool::OnDropLeaveField (0x4DA981) reads only the mode, object
	// and pickup actor for mode2. There is no reserved or pet-data tail.
	return w.Packet()
}

// RemoveItemExploded is CDropPool::OnDropLeaveField mode 4 (Meso Explosion):
// object ID and a U16 delay in milliseconds.
func RemoveItemExploded(objectID int32, delayMS int) []byte {
	if objectID <= 0 {
		return nil
	}
	w := NewWriter(SendRemoveItem)
	w.U8(4)
	w.I32(objectID)
	w.U16(uint16(max(0, min(0xffff, delayMS))))
	return w.Packet()
}

// RemoveItemExpired matches Legacy CField.removeItemFromMap animation zero.
func RemoveItemExpired(objectID int32) []byte {
	if objectID <= 0 {
		return nil
	}
	w := NewWriter(SendRemoveItem)
	w.U8(0)
	w.I32(objectID)
	return w.Packet()
}

// ReadMoney reads one meso amount the client entered: a trade offer or a
// storage transfer. It is eight bytes under the long money contract.
func ReadMoney(r *Reader) int64 {
	if domain.LongMoneyEnabled() {
		return r.I64()
	}
	return int64(r.I32())
}

// writeMoney writes a balance: mesos held, storage mesos, or a cash wallet
// value. It is eight bytes under the long money contract, else four.
func writeMoney(w *Writer, value int64) {
	if domain.LongMoneyEnabled() {
		w.I64(value)
		return
	}
	w.I32(int32(value))
}
