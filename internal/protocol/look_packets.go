package protocol

import "maple-go-server/internal/domain"

// UpdateLookStats reports a beauty salon change to the owner: stat mask
// 0x2 FACE and 0x4 HAIR, each an I32 in native bit order, unlocking actions.
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
