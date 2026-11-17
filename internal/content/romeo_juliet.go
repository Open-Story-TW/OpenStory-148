package content

const (
	RomeoPQName    = "RomeoPQ"
	JulietPQName   = "JulietPQ"
	RomeoLabLiquid = 4001132
	RomeoLabBeaker = 2618000
)

func RomeoJulietEvent(name string) bool {
	return name == RomeoPQName || name == JulietPQName
}

func RomeoSearchNPC(mapID, npc int) bool {
	switch mapID {
	case 926100000, 926100203:
		return npc == 2112007
	case 926110000, 926110203:
		return npc == 2112013
	}
	return false
}

func RomeoLabReactor(mapID, templateID int) bool {
	switch mapID {
	case 926100000, 926110000:
		return templateID == 2618005
	case 926100100, 926110100:
		return templateID == 2618000 || templateID == 2618006 || templateID == 2618009 || templateID == 2618010
	case 926100200, 926110200:
		return templateID == 2618001 || templateID == 2618002 || templateID == 2618007
	case 926100201, 926110201:
		return templateID == 2612002 || templateID == 2612003 || templateID == 2618003
	case 926100202, 926110202:
		return templateID == 2612001 || templateID == 2612003 || templateID == 2618004 || templateID == 2518000
	case 926100203, 926110203:
		return templateID == 2618005
	}
	return false
}

// The two factions share the same seven objective rooms and keep their own
// entrance and exit maps. Quest 1205 describes exactly four level 70+ players.
func romeoJulietEvent(name string, lobby, first int) Event {
	madman := 2112000
	if name == JulietPQName {
		madman = 2112010
	}
	return Event{
		Name: name, LobbyMap: lobby, ExitMap: first + 700,
		Maps: []int{
			first, first + 1, first + 100, first + 200, first + 201, first + 202, first + 203,
			first + 300, first + 301, first + 302, first + 303, first + 304,
			first + 400, first + 401, first + 500, first + 600,
		},
		Seconds: 1200, PartyMin: 4, PartyMax: 4, Keep: 4, LevelMin: 70, LevelMax: 255,
		Spawns:       []EventSpawn{{MapID: first + 203, Life: Life{Kind: "n", TemplateID: madman, X: 200, Y: 188}}},
		RequiredMobs: map[int]map[int]int{first + 1: {9300145: 23, 9300146: 19}},
	}
}
