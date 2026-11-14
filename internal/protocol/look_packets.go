package protocol

import "maple-go-server/internal/domain"

// UpdateLookStats reports a beauty salon change to the owner: stat mask
// 0x2 FACE and 0x4 HAIR, each an I32 in native bit order, unlocking actions.
// UpdateFame is the fame field of the same stat update (mask 0x20000, the
// I32 the character stat block carries).
func UpdateFame(fame int) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x20000)
	w.I32(int32(fame))
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

// UpdateSkin is the skin field (mask 0x1, one byte).
func UpdateSkin(skin int) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x1)
	w.U8(uint8(skin))
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

func UpdateLookStats(c domain.Character, face, hair bool) []byte {
	if !face && !hair {
		return nil
	}
	mask := int64(0)
	if face {
		mask |= 0x2
	}
	if hair {
		mask |= 0x4
	}
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(mask)
	if face {
		w.I32(int32(c.Face))
	}
	if hair {
		w.I32(int32(c.Hair))
	}
	w.U8(0)
	w.U8(0)
	return w.Packet()
}
