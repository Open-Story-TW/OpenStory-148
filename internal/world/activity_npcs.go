package world

import (
	"slices"

	"maple-go-server/internal/content"
)

// v148 has these ticketing attendants without map life entries. Place
// each attendant on a ground platform at its departure terminal.
var activityNPCs = map[int]content.Life{
	103000000: {Kind: "n", TemplateID: 9270041, X: 840, Y: 66, Foothold: 94, RX0: 840, RX1: 840},
	540010000: {Kind: "n", TemplateID: 9270038, X: 84, Y: 275, Foothold: 1, RX0: 84, RX1: 84},
}

func activityLife(m content.Map) []content.Life {
	npc, needed := activityNPCs[m.ID]
	if !needed {
		return m.Life
	}
	for _, life := range m.Life {
		if !life.Hidden && life.Kind == "n" && life.TemplateID == npc.TemplateID {
			return m.Life
		}
	}
	for _, ground := range m.Footholds {
		if ground.ID == npc.Foothold && ground.X1 <= npc.X && npc.X <= ground.X2 &&
			ground.Y1 == npc.Y && ground.Y2 == npc.Y {
			return append(slices.Clone(m.Life), npc)
		}
	}
	return m.Life
}
