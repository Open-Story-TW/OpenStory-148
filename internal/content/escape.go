package content

const (
	EscapeName        = "Prison"
	EscapeLobby       = 921160000
	EscapeTower       = 921160100
	EscapeApproach    = 921160200
	EscapeMazeFirst   = 921160300
	EscapeMazeLast    = 921160350
	EscapePassage     = 921160400
	EscapeTraps       = 921160500
	EscapePrison      = 921160600
	EscapeBossRoom    = 921160700
	EscapeLobbyNPC    = 9020005
	EscapeCaptiveNPC  = 9020006
	EscapeBoss        = 9300454
	EscapeKey         = 4001528
	EscapeRewardKey   = 4001534
	EscapeCommonChest = 9211000
	EscapeKeyChest    = 9212000
	EscapePrisonDoor  = 9218000
	EscapeFinalGate   = 9219000
)

func escapeEvent() Event {
	// TWMS v148 Quest.wz/1215 defines the seven stages, 20 minutes, and
	// 3–6 participants at level 120 or above.
	stages := []int{EscapeTower, EscapeApproach}
	required := map[int]map[int]int{
		EscapeTower:    {},
		EscapeApproach: {9300450: 6, 9300451: 6},
		EscapePassage:  {9300450: 7, 9300452: 9},
		EscapeTraps:    {},
		EscapeBossRoom: {},
	}
	for id := EscapeMazeFirst; id <= EscapeMazeLast; id += 10 {
		stages = append(stages, id)
		required[id] = map[int]int{9300449: 4}
	}
	stages = append(stages, EscapePassage, EscapeTraps, EscapePrison, EscapeBossRoom)
	return Event{
		Name: EscapeName, Maps: stages, LobbyMap: EscapeLobby, ExitMap: EscapeLobby,
		Seconds: 1200, PartyMin: 3, PartyMax: 6, Keep: 1, LevelMin: 120, LevelMax: 255,
		RequiredMobs: required,
		Spawns: []EventSpawn{
			{MapID: EscapeBossRoom, Life: Life{Kind: "m", TemplateID: EscapeBoss,
				X: -2161, Y: -181, Foothold: 6, RX0: -2183, RX1: -1736, RespawnSeconds: -1}},
			// The static 9020008 in the boss room is hidden. The v148
			// prisonBreak_PQ script belongs to the spawned captive 9020006.
			{MapID: EscapeBossRoom, Life: Life{Kind: "n", TemplateID: EscapeCaptiveNPC,
				X: -2161, Y: -181, Foothold: 6, RX0: -2163, RX1: -2111}},
		},
	}
}

func EscapeMazeMap(id int) bool {
	return id >= EscapeMazeFirst && id <= EscapeMazeLast && (id-EscapeMazeFirst)%10 == 0
}

func EscapeReactor(mapID, id int) bool {
	if mapID != EscapePrison {
		return false
	}
	switch id {
	case EscapeCommonChest, EscapeKeyChest, EscapePrisonDoor, EscapeFinalGate:
		return true
	}
	return false
}

func EscapeKeyMob(mapID, mobID int) bool {
	return mapID == EscapePrison && mobID == 9300453
}
