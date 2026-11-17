package content

const (
	MalaysiaBossName     = "MalaysiaBoss"
	MalaysiaBossLobby    = 551030100
	MalaysiaBossArena    = 551030200
	MalaysiaBossOffering = 4032246
)

type MalaysiaBossStatue struct {
	Reactor, X, Y, Foothold int
	FirstForm, LastForm     int
	OfferingArea            MapArea
}

// The two statues have independent WZ offerings and revive chains.
func MalaysiaBossStatues() [2]MalaysiaBossStatue {
	return [2]MalaysiaBossStatue{
		{Reactor: 5511000, X: -527, Y: 637, Foothold: 37, FirstForm: 9420541, LastForm: 9420544,
			OfferingArea: MapArea{Left: -572, Top: 600, Right: -446, Bottom: 647}},
		{Reactor: 5511001, X: -238, Y: 636, Foothold: 39, FirstForm: 9420546, LastForm: 9420549,
			OfferingArea: MapArea{Left: -288, Top: 602, Right: -160, Bottom: 645}},
	}
}

func MalaysiaOfferingStatue(x, y int) (MalaysiaBossStatue, bool) {
	for _, statue := range MalaysiaBossStatues() {
		if statue.OfferingArea.Contains(x, y) {
			return statue, true
		}
	}
	return MalaysiaBossStatue{}, false
}
