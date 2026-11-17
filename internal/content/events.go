package content

import "strconv"

// Event describes one instanced activity (party quest, boss fight) that a
// single group holds on a channel at a time. Its stage maps are the channel's
// ordinary maps: while a group is inside nobody else may enter, as with the
// Legacy event manager's "state" property.
type Event struct {
	Name     string
	LobbyMap int // Zero for events whose entrance is selected by their script.
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
	Ticket         int
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

var events = eventDefinitions()

func eventDefinitions() map[string]Event {
	definitions := map[string]Event{
		RomeoPQName:     romeoJulietEvent(RomeoPQName, 261000011, 926100000),
		JulietPQName:    romeoJulietEvent(JulietPQName, 261000021, 926110000),
		IceKnightName:   iceKnightEvent(),
		EscapeName:      escapeEvent(),
		HoblinPQName:    hoblinPQEvent(),
		EllinPQName:     ellinPQEvent(),
		PiratePQName:    piratePQEvent(),
		KentaPQName:     kentaEvent(),
		DragonRiderName: dragonRiderEvent(),
		LudibriumPQName: ludibriumEvent(),
		ZakumMineName:   zakumMineEvent(),
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
		"HillaHard": {
			Name: "HillaHard", Maps: []int{262031100, 262031200, 262031300},
			ExitMap: 262010000, Seconds: 3600, PartyMin: 2, PartyMax: 6, Keep: 2,
			LevelMin: 170, LevelMax: 200, ExpeditionType: 2010, LobbyMap: 262030000, RevivalHP: 50,
			RequiredMobs: map[int]map[int]int{262031100: {8870101: 8}, 262031200: {8870102: 8}, 262031300: {8870100: 1}},
		},
		"BalrogEasy": {
			Name: "BalrogEasy", Maps: []int{105100400, 105100401}, ExitMap: 105100100,
			Seconds: 1200, PartyMin: 1, PartyMax: 6, Keep: 1,
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
			Seconds: 3000, PartyMin: 5, PartyMax: 30, Keep: 5,
			LevelMin: 50, LevelMax: 200, ExpeditionType: 2002, LobbyMap: 211042400,
			RequiredMobs: map[int]map[int]int{280030000: {}},
		},
		MalaysiaBossName: {
			Name: MalaysiaBossName, Maps: []int{MalaysiaBossArena}, ExitMap: MalaysiaBossLobby, LobbyMap: MalaysiaBossLobby,
			Seconds: 3600, PartyMin: 2, PartyMax: 6, Keep: 2, LevelMin: 90, LevelMax: 200,
			RequiredMobs: map[int]map[int]int{MalaysiaBossArena: {}},
		},
		"Papulatus": {
			Name: "Papulatus", Maps: []int{PapulatusArena}, ExitMap: PapulatusLobby,
			Seconds: 2700, PartyMin: 2, PartyMax: 6, Keep: 2, LevelMin: 15, LevelMax: 255,
			LobbyMap: PapulatusLobby, RequiredMobs: map[int]map[int]int{PapulatusArena: {}},
		},
		"ChaosZakum": {
			Name: "ChaosZakum", Maps: []int{280030001}, ExitMap: 211042301,
			Seconds: 4500, PartyMin: 5, PartyMax: 30, Keep: 5,
			LevelMin: 100, LevelMax: 200, ExpeditionType: 2005, LobbyMap: 211042401,
			RequiredMobs: map[int]map[int]int{280030001: {}},
		},
		"Cygnus": {
			Name: "Cygnus", Maps: []int{271040100}, ExitMap: 271040210,
			Seconds: 3600, PartyMin: 3, PartyMax: 18, Keep: 3,
			LevelMin: 170, LevelMax: 200, ExpeditionType: 2008, LobbyMap: 271040000,
			RequiredMobs: map[int]map[int]int{271040100: {}},
		},
		"PinkBean": {
			Name: "PinkBean", Maps: []int{270050100}, ExitMap: 270050300,
			Seconds: 3600, PartyMin: 3, PartyMax: 30, Keep: 3,
			LevelMin: 120, LevelMax: 200, ExpeditionType: 2004, LobbyMap: 270050000,
			RequiredMobs: map[int]map[int]int{270050100: {8820019: 1, 8820020: 1, 8820021: 1, 8820022: 1, 8820023: 1}},
			Spawns: []EventSpawn{{MapID: 270050100, Life: Life{Kind: "n", TemplateID: 2141000,
				X: -190, Y: -42, Foothold: 35, RX0: -240, RX1: -140}}},
		},
		"Horntail": {
			Name: "Horntail", Maps: []int{240060000, 240060100, 240060200}, ExitMap: 240000000,
			Seconds: 9000, PartyMin: 6, PartyMax: 30, Keep: 6, LevelMin: 80, LevelMax: 200,
			ExpeditionType: 2003, LobbyMap: 240050400,
			RequiredMobs: map[int]map[int]int{240060000: {}, 240060100: {}, 240060200: {}},
		},
		"ChaosHorntail": {
			Name: "ChaosHorntail", Maps: []int{240060001, 240060101, 240060201}, ExitMap: 240000000,
			Seconds: 9000, PartyMin: 6, PartyMax: 30, Keep: 6, LevelMin: 110, LevelMax: 200,
			ExpeditionType: 2006, LobbyMap: 240050400,
			RequiredMobs: map[int]map[int]int{240060001: {}, 240060101: {}, 240060201: {}},
		},
		"VonLeon": {
			Name: "VonLeon", Maps: []int{211070100}, ExitMap: 211061001,
			Seconds: 3600, PartyMin: 6, PartyMax: 18, Keep: 6,
			LevelMin: 120, LevelMax: 200, ExpeditionType: 2007, LobbyMap: 211070000, RevivalHP: 50,
			RequiredMobs: map[int]map[int]int{211070100: {}},
			Spawns: []EventSpawn{{MapID: 211070100, Life: Life{Kind: "n", TemplateID: 2161000,
				X: 0, Y: -181, Foothold: 1, RX0: -75, RX1: 75}}},
		},
		"Arkarium": {
			Name: "Arkarium", Maps: []int{272030400}, ExitMap: 272020110,
			Seconds: 3000, PartyMin: 6, PartyMax: 18, Keep: 6,
			LevelMin: 120, LevelMax: 200, ExpeditionType: 2009, LobbyMap: 272020110, RevivalHP: 50,
			RequiredMobs: map[int]map[int]int{272030400: {9300304: 1}},
			Spawns: []EventSpawn{{MapID: 272030400, Life: Life{Kind: "n", TemplateID: 2144010,
				X: 0, Y: -181, Foothold: 8, RX0: -75, RX1: 75}}},
		},
	}
	// The ticket descriptions set the level bands. The end bosses in the
	// v148 Monster Park quests identify routes across the map ID prefixes.
	for _, tier := range []struct {
		levelMin, levelMax, ticket int
		routes                     []int
	}{
		{30, 95, 4001514, []int{952000000, 952010000, 952040000, 952020000, 952030000, 953000000, 953010000}},
		{135, 165, 4001516, []int{953020000, 953030000, 953040000, 953050000, 954000000}},
		{160, 200, 4001522, []int{954010000, 954020000, 954030000, 954040000, 954050000}},
	} {
		for _, start := range tier.routes {
			name := "MonsterPark_" + strconv.Itoa(start)
			stages := make([]int, 6)
			for i := range stages {
				stages[i] = start + 100*i
			}
			definitions[name] = Event{
				Name: name, Maps: stages, LobbyMap: 951000000, ExitMap: 951000000, Ticket: tier.ticket,
				Seconds: 1200, PartyMin: 1, PartyMax: 6, Keep: 1, LevelMin: tier.levelMin, LevelMax: tier.levelMax,
			}
		}
	}
	return definitions
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
	if e, ok := events[name]; ok {
		return e, true
	}
	c, ok := ChristmasByName(name)
	return c.Event, ok
}

// EventByMap finds the event that owns a stage map.
func EventByMap(mapID int) (Event, bool) {
	if name := eventByMap[mapID]; name != "" {
		return EventByName(name)
	}
	c, ok := ChristmasByMap(mapID)
	return c.Event, ok
}
