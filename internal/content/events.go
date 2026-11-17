package content

// Event describes one instanced activity (party quest, boss fight) that a
// single group holds on a channel at a time. Its stage maps are the channel's
// ordinary maps: while a group is inside nobody else may enter, as with the
// Legacy event manager's "state" property.
type Event struct {
	Name string
	// Maps are the stage maps; the group enters Maps[0] at EntryPortal.
	Maps        []int
	EntryPortal int
	// ExitMap receives everyone when the event ends, and anyone found on a
	// stage map without belonging to the running group.
	ExitMap int
	// Seconds is the time limit shown on the client's clock.
	Seconds int
	// PartyMin members must stand on the leader's map to start, and the event
	// ends as soon as fewer than Keep remain inside.
	PartyMin, PartyMax int
	Keep               int
	LevelMin, LevelMax int
	Spawns             []EventSpawn
	// ExpeditionType binds admission to a native expedition instead of a
	// single party. Expedition entries are restricted to their lobby map.
	ExpeditionType int
	LobbyMap       int
	// RevivalHP is used when the raid revives the player on the same map.
	RevivalHP int
	// Exact static encounter populations needed from the mounted v148 maps.
	RequiredMobs map[int]map[int]int
}

// EventSpawn is a scripted spawn added each time the event's map is reset.
type EventSpawn struct {
	MapID int
	Life  Life
	// Prelude is a nonlethal opening phase. Killing the real monster is
	// possible only after the prelude ends and the WZ HP is restored.
	Prelude bool
}

func (e Event) Stage(mapID int) bool {
	for _, id := range e.Maps {
		if id == mapID {
			return true
		}
	}
	return false
}

var events = map[string]Event{
	HenesysPQName: {
		Name: HenesysPQName, Maps: []int{HenesysPQMap},
		ExitMap: HenesysPQLobby, Seconds: 600, PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 50, LevelMax: 255,
	},
	// 第一次同行 (Legacy event/KerningPQ.js): five stages and the bonus map.
	"KerningPQ": {
		Name: "KerningPQ", Maps: []int{910340100, 910340200, 910340300, 910340400, 910340500, 910340600},
		ExitMap: 910340000, Seconds: 1200, PartyMin: 3, PartyMax: 6, Keep: 2, LevelMin: 20, LevelMax: 255,
		// The final map has no WZ life entry for King Slime. Spawn it on
		// the lower platform between the two NPCs.
		Spawns: []EventSpawn{{MapID: 910340500, Life: Life{Kind: "m", TemplateID: 9300003,
			X: 35, Y: -435, Foothold: 53, RX0: 0, RX1: 90, RespawnSeconds: -1}}},
	},
	"Hilla": {
		Name: "Hilla", Maps: []int{262030100, 262030200, 262030300},
		ExitMap: 262010000, Seconds: 3600, PartyMin: 2, PartyMax: 6, Keep: 2,
		LevelMin: 120, LevelMax: 200, ExpeditionType: 2010, LobbyMap: 262030000, RevivalHP: 50,
		RequiredMobs: map[int]map[int]int{262030100: {8870001: 8}, 262030200: {8870002: 8}, 262030300: {8870000: 1}},
	},
	"BalrogEasy": {
		Name: "BalrogEasy", Maps: []int{105100400, 105100401}, ExitMap: 105100100,
		Seconds: 1200, PartyMin: 3, PartyMax: 6, Keep: 3,
		LevelMin: 45, LevelMax: 200, ExpeditionType: 2000, LobbyMap: 105100100,
		RequiredMobs: map[int]map[int]int{105100400: {}, 105100401: {}},
		Spawns: []EventSpawn{
			{MapID: 105100400, Life: Life{Kind: "m", TemplateID: 8830007, X: 416, Y: 258, Foothold: 3, RespawnSeconds: -1}, Prelude: true},
			{MapID: 105100400, Life: Life{Kind: "m", TemplateID: 8830008, X: 416, Y: 258, Foothold: 3, RespawnSeconds: -1}, Prelude: true},
			{MapID: 105100400, Life: Life{Kind: "m", TemplateID: 8830009, X: 416, Y: 258, Foothold: 3, RespawnSeconds: -1}, Prelude: true},
		},
	},
	"Zakum": {
		Name: "Zakum", Maps: []int{280030000}, ExitMap: 211042300,
		Seconds: 3000, PartyMin: 2, PartyMax: 30, Keep: 2,
		LevelMin: 50, LevelMax: 200, ExpeditionType: 2002, LobbyMap: 211042400,
		RequiredMobs: map[int]map[int]int{280030000: {}},
	},
	"Papulatus": {
		Name: "Papulatus", Maps: []int{PapulatusArena}, ExitMap: PapulatusLobby,
		Seconds: 2700, PartyMin: 2, PartyMax: 6, Keep: 2, LevelMin: 15, LevelMax: 255,
		LobbyMap: PapulatusLobby, RequiredMobs: map[int]map[int]int{PapulatusArena: {}},
	},
	"ChaosZakum": {
		Name: "ChaosZakum", Maps: []int{280030001}, ExitMap: 211042301,
		Seconds: 4500, PartyMin: 3, PartyMax: 30, Keep: 3,
		LevelMin: 100, LevelMax: 200, ExpeditionType: 2005, LobbyMap: 211042401,
		RequiredMobs: map[int]map[int]int{280030001: {}},
	},
	"VonLeon": {
		Name: "VonLeon", Maps: []int{211070100}, ExitMap: 211061001,
		Seconds: 3600, PartyMin: 3, PartyMax: 18, Keep: 3,
		LevelMin: 120, LevelMax: 200, ExpeditionType: 2007, LobbyMap: 211070000, RevivalHP: 50,
		RequiredMobs: map[int]map[int]int{211070100: {}},
		Spawns: []EventSpawn{{MapID: 211070100, Life: Life{Kind: "n", TemplateID: 2161000,
			X: 0, Y: -181, Foothold: 1, RX0: -75, RX1: 75}}},
	},
	"Arkarium": {
		Name: "Arkarium", Maps: []int{272030400}, ExitMap: 272020110,
		Seconds: 3000, PartyMin: 3, PartyMax: 18, Keep: 3,
		LevelMin: 120, LevelMax: 200, ExpeditionType: 2009, LobbyMap: 272020110, RevivalHP: 50,
		RequiredMobs: map[int]map[int]int{272030400: {9300304: 1}},
		Spawns: []EventSpawn{{MapID: 272030400, Life: Life{Kind: "n", TemplateID: 2144010,
			X: 0, Y: -181, Foothold: 8, RX0: -75, RX1: 75}}},
	},
}

var eventByMap = func() map[int]string {
	out := map[int]string{}
	for name, e := range events {
		for _, id := range e.Maps {
			out[id] = name
		}
	}
	return out
}()

func EventByName(name string) (Event, bool) {
	e, ok := events[name]
	return e, ok
}

// EventByMap finds the event that owns a stage map.
func EventByMap(mapID int) (Event, bool) {
	e, ok := events[eventByMap[mapID]]
	return e, ok
}
