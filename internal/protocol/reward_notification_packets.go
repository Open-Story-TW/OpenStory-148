package protocol

import "maple-go-server/internal/domain"

// SendStatusInfo is CWvsContext's status-message dispatcher, also used for
// quest updates. Inventory/stat updates alone do not create gain messages.
const SendStatusInfo uint16 = 0x36

// ShowItemGain matches Legacy CWvsContext.InfoPacket.getShowItemGain(..., false):
// status kind 0, item kind 0, template I32, acquired quantity I32. Quantity is
// the acquired amount, not the resulting inventory-stack quantity.
func ShowItemGain(templateID, quantity int) []byte {
	if templateID <= 0 || templateID > 2147483647 || quantity <= 0 || quantity > 2147483647 {
		return nil
	}
	w := NewWriter(SendStatusInfo)
	w.U8(0)
	w.U8(0)
	w.I32(int32(templateID))
	w.I32(int32(quantity))
	return w.Packet()
}

// ShowMesoGain is the non-chat pickup message (kind 0, meso kind 1).
func ShowMesoGain(gain int64) []byte {
	if gain <= 0 || gain > 2147483647 {
		return nil
	}
	w := NewWriter(SendStatusInfo)
	w.U8(0)
	w.U8(1)
	w.U8(0) // No missing portion.
	w.I32(int32(gain))
	w.U16(0) // No separately awarded internet-cafe bonus.
	return w.Packet()
}

// ShowExperienceGain matches Legacy GainEXP_Others(gain, false, white), and
// GainEXP_Monster with all separately reported bonuses zero. The caller supplies
// the committed total; it must not infer it from an EXP counter across level-up.
func ShowExperienceGain(gain int64, white bool) []byte {
	if gain <= 0 || gain > domain.MaxExperience() {
		return nil
	}
	w := NewWriter(SendStatusInfo)
	w.U8(3)
	if white {
		w.U8(1)
	} else {
		w.U8(0)
	}
	writeEXP(w, gain)
	w.U8(0)  // Non-chat message.
	w.I32(0) // Event EXP (native 0x9475D5).
	w.U8(0)  // Party bonus rate: zero skips its conditional extra byte.
	w.U8(0)  // Reserved (0x9475E5).
	w.I32(0) // Wedding EXP.
	w.I32(0) // Party base EXP.
	// inChat=0 above skips the two conditional chat/rate bytes at
	// 0x947622/0x947637. The next byte is unconditional (0x94764C).
	w.U8(0)
	// Remaining bonus amounts: twelve in v145 (0x947656..0x9476C2); v148
	// (0x9E22E0) reads a thirteenth after the seventh.
	for i := 0; i < 13; i++ {
		w.I32(0)
	}
	w.U8(0) // Java GainEXP_Monster's trailing zero, not read by this native branch.
	return w.Packet()
}
