package protocol

import "maple-go-server/internal/domain"

const RecvKeymap uint16 = 0xea

func KeymapPacket(mapping domain.Keymap) []byte {
	w := NewWriter(SendKeymap)
	w.U8(0)
	for _, b := range mapping {
		if !domain.ValidKeyBinding(b) {
			return nil
		}
		w.U8(b.Type)
		w.I32(b.Action)
	}
	return w.Packet()
}

// Native OnInit sends mode0/count89 followed by key/type/action records.
// Pet auto-potion settings (mode1/2) belong to a different feature.
func ReadKeymapChanges(r *Reader) ([]domain.KeyChange, error) {
	if r.I32() != 0 {
		return nil, ErrMalformed
	}
	n := int(r.U32())
	if n < 0 || n > domain.KeymapSlots || r.Remaining() != n*9 {
		return nil, ErrMalformed
	}
	seen := map[int]bool{}
	result := make([]domain.KeyChange, 0, n)
	for range n {
		key := int(r.I32())
		b := domain.KeyBinding{Type: r.U8(), Action: r.I32()}
		if key < 0 || key >= domain.KeymapSlots || seen[key] || !domain.ValidKeyBinding(b) {
			return nil, ErrMalformed
		}
		seen[key] = true
		result = append(result, domain.KeyChange{Key: key, Binding: b})
	}
	if r.Err() != nil {
		return nil, ErrMalformed
	}
	return result, nil
}
