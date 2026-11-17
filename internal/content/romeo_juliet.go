package content

const (
	RomeoPQName  = "RomeoPQ"
	JulietPQName = "JulietPQ"
)

func RomeoLabReactor(mapID, templateID int) bool {
	switch mapID {
	case 926100000, 926110000:
		return templateID == 2618005
	case 926100100, 926110100:
		return templateID == 2618000 || templateID == 2618006 || templateID == 2618009 || templateID == 2618010
	}
	return false
}

// The two factions share the same seven objective rooms and keep their own
// entrance and exit maps. Quest 1205 describes exactly four level 70+ players.
func romeoJulietEvent(name string, lobby, first int) Event {
	return Event{
		Name: name, LobbyMap: lobby, ExitMap: first + 700,
		Maps: []int{
			first, first + 1, first + 100, first + 200, first + 201, first + 202, first + 203,
			first + 300, first + 301, first + 302, first + 303, first + 304,
			first + 400, first + 401, first + 500, first + 600,
		},
		Seconds: 1200, PartyMin: 4, PartyMax: 4, Keep: 4, LevelMin: 70, LevelMax: 255,
		RequiredMobs: map[int]map[int]int{first + 1: {9300145: 23, 9300146: 19}},
	}
}
