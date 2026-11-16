// v145 native PLAYER_INTERACTION layouts; see Open-Maple docs/server/baseline/native-trade.json.
package protocol

import (
	"errors"
	"maple-go-server/internal/domain"
)

const RecvPlayerInteraction uint16 = 0xda
const SendPlayerInteraction uint16 = 0x24b

var ErrUnsupportedTradeAction = errors.New("unsupported player interaction action")

func TradeInvite(name string, reference uint32) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(11)
	w.U8(3)
	w.String(name)
	w.U32(reference)
	return w.Packet()
}
func TradeOpen(self domain.Character, partner *domain.Character, seat byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(10)
	w.U8(3)
	w.U8(2)
	w.U8(seat)
	if seat == 1 && partner != nil {
		w.U8(0)
		addCharLookMode(w, *partner, false)
		w.String(partner.Name)
		w.U16(uint16(partner.Job))
	}
	w.U8(seat)
	addCharLookMode(w, self, false)
	w.String(self.Name)
	w.U16(uint16(self.Job))
	w.U8(255)
	return w.Packet()
}
func TradeVisitor(character domain.Character) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(9)
	w.U8(1)
	addCharLookMode(w, character, false)
	w.String(character.Name)
	w.U16(uint16(character.Job))
	return w.Packet()
}
func TradeItem(side byte, item domain.TradeItem) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(0)
	w.U8(side)
	w.U8(byte(item.TradeSlot))
	if item.Equipment != nil {
		addEquipment(w, *item.Equipment)
	} else if item.Item != nil {
		copy := *item.Item
		copy.Quantity = item.Quantity
		addBundleItem(w, copy)
	} else {
		return nil
	}
	return w.Packet()
}
func TradeMesos(side byte, mesos int64) []byte {
	if mesos < 0 || mesos > domain.MaxMoney() {
		return nil
	}
	w := NewWriter(SendPlayerInteraction)
	w.U8(1)
	w.U8(side)
	writeMoney(w, mesos)
	return w.Packet()
}
func TradeConfirm() []byte { w := NewWriter(SendPlayerInteraction); w.U8(2); return w.Packet() }
func TradeClose(seat, reason byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(18)
	w.U8(seat)
	w.U8(reason)
	return w.Packet()
}

// TradeCheck is the native UI's item-info check list. It is not authority to
// grant assets: settlement uses the session-fenced server inventory rows.
type TradeCheck struct {
	ItemID   int
	Checksum uint32
}
type TradeRequest struct {
	Action                             byte
	TargetID                           int64
	Reference                          uint32
	InvType, Slot, Quantity, TradeSlot int
	Mesos                              int64
	Checks                             []TradeCheck
}

func ReadTradeRequest(r *Reader) (TradeRequest, error) {
	v := TradeRequest{Action: r.U8()}
	switch v.Action {
	case 6: // original4F2161 create: roomType3 plus reserved zero
		if r.U8() != 3 || r.U8() != 0 {
			return v, ErrMalformed
		}
	case 11:
		v.TargetID = int64(r.I32())
		if v.TargetID <= 0 {
			return v, ErrMalformed
		}
	case 9: // original5CAEAF accept
		v.Reference = r.U32()
		if v.Reference == 0 || r.U8() != 0 || r.U8() != 0 {
			return v, ErrMalformed
		}
	case 12: // originalNo/timeout3 or original busy rejection4
		v.Reference = r.U32()
		reason := r.U8()
		if v.Reference == 0 || (reason != 3 && reason != 4) {
			return v, ErrMalformed
		}
	case 0:
		v.InvType = int(r.U8())
		v.Slot = int(r.I16())
		v.Quantity = int(r.I16())
		v.TradeSlot = int(r.U8())
		// The cash tab (5) is read here; whether it may be offered is the
		// trade_cash_items setting, checked where the offer is built.
		if v.InvType < 1 || v.InvType > 5 || v.Slot < 1 || v.Quantity < 1 || v.TradeSlot < 1 || v.TradeSlot > 9 {
			return v, ErrMalformed
		}
	case 1:
		v.Mesos = ReadMoney(r)
		if v.Mesos <= 0 {
			return v, ErrMalformed
		}
	case 2, 5: // original6EEFC3 local confirmation /6EEB18 remote check ack
		count := int(r.U8())
		if count > 9 {
			return v, ErrMalformed
		}
		for n := 0; n < count; n++ {
			check := TradeCheck{ItemID: int(r.I32()), Checksum: r.U32()}
			if check.ItemID <= 0 {
				return v, ErrMalformed
			}
			v.Checks = append(v.Checks, check)
		}
	case 18:
	default:
		return v, ErrUnsupportedTradeAction
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return v, ErrMalformed
	}
	return v, nil
}
