package world

import (
	"slices"

	"maple-go-server/internal/content"
)

// v148 has these NPC templates without map life entries. Place the two
// ticketing attendants and the rock-paper-scissors host on ground platforms
// in their respective towns.
var activityNPCs = map[int]content.Life{
	103000000: {Kind: "n", TemplateID: 9270041, X: 840, Y: 66, Foothold: 94, RX0: 840, RX1: 840},
	540010000: {Kind: "n", TemplateID: 9270038, X: 84, Y: 275, Foothold: 1, RX0: 84, RX1: 84},
	100000000: {Kind: "n", TemplateID: rpsNPC, X: 390, Y: 274, Foothold: 113, RX0: 390, RX1: 390},
}

func activityLife(m content.Map, judgement bool) []content.Life {
	npc, needed := activityNPCs[m.ID]
	if judgement && m.ID == content.JudgementLobby {
		npc, needed = content.Life{Kind: "n", TemplateID: content.JudgementNPC, X: -235, Y: -473, Foothold: 2, RX0: -235, RX1: -235}, true
	}
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
