package content

// These two field bosses use server-side spawns. Their v148 maps supply the
// regular monsters and terrain, and Mob.wz supplies the bosses' combat stats.
func addMuLungBossSpawn(m *Map) {
	var spawn Life
	switch m.ID {
	case 250010304: // Territory of Wandering Bear, Tae Roon.
		spawn = Life{TemplateID: 7220000, X: -234, Y: 393, Foothold: 53, RX0: -750, RX1: 430, RespawnSeconds: 2100}
	case 250010503: // Goblin Forest, King Sage Cat.
		spawn = Life{TemplateID: 7220002, X: 227, Y: 543, Foothold: 80, RX0: -600, RX1: 760, RespawnSeconds: 1800}
	default:
		return
	}
	spawn.Kind = "m"
	for _, existing := range m.Life {
		if existing.Kind == spawn.Kind && existing.TemplateID == spawn.TemplateID && !existing.Hidden {
			return
		}
		spawn.Index = max(spawn.Index, existing.Index+1)
	}
	m.Life = append(m.Life, spawn)
}
