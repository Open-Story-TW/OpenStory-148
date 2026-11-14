package protocol

import "maple-go-server/internal/domain"

// Native CWvsContext::OnPacket case0x28 -> 0x936713 decodes exactly two U8s:
// inventory type and new capacity. The existing character-info list is also U8.
func InventoryCapacityUpdate(invType, capacity int) []byte {
	if invType < 1 || invType > 5 || capacity < domain.DefaultInventorySlots || capacity > domain.MaxInventorySlots {
		return nil
	}
	w := NewWriter(0x2b)
	w.U8(byte(invType))
	w.U8(byte(capacity))
	return w.Packet()
}
