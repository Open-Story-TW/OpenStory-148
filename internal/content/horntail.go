package content

const HorntailDisguiseItem = 2210003

// HorntailLoot identifies the eight visible parts and the linked reward
// template. The normal pool is one WZ mob. The chaos pool finishes at 8810122.
func HorntailLoot(mapID int) (firstPart, rewardMob int, ok bool) {
	switch mapID {
	case 240060200:
		return 8810002, 8810018, true
	case 240060201:
		return 8810102, 8810122, true
	default:
		return 0, 0, false
	}
}
