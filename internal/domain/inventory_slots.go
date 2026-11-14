package domain

const DefaultInventorySlots = 24
const MaxInventorySlots = 96

// Capacity maps the native inventory types 1..5 to per-character bag sizes.
// Omitted capacity metadata retains the original 24-slot starter inventory.
func (c Character) Capacity(invType int) int {
	if invType < 1 || invType > 5 {
		return 0
	}
	value := c.InventorySlots[invType-1]
	if value == 0 {
		return DefaultInventorySlots
	}
	if value < DefaultInventorySlots || value > MaxInventorySlots {
		return 0
	}
	return value
}
