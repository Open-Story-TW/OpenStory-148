package content

import "fmt"

const (
	HoblinPQName       = "Rex"
	HoblinTown         = 211000002
	HoblinLobby        = 921120000
	HoblinWaiting      = 921120001
	HoblinAmbush       = 921120005
	HoblinEscortFirst  = 921120100
	HoblinEscortSecond = 921120200
	HoblinArena        = 921120300
	HoblinResult       = 921120400
	HoblinEscort       = 9300275
	HoblinArenaEscort  = 9300282
	HoblinKing         = 9300281
	HoblinReward       = 4001530
	HoblinGlacialWater = 2022698
)

func hoblinPQEvent() Event {
	return Event{
		Name: HoblinPQName, ExitMap: HoblinWaiting, Seconds: 1800,
		PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 120, LevelMax: 255,
		Maps: []int{HoblinAmbush, HoblinEscortFirst, HoblinEscortSecond, HoblinArena},
		Spawns: []EventSpawn{
			{MapID: HoblinEscortFirst, Life: Life{Kind: "m", TemplateID: HoblinEscort, X: -453, Y: 153,
				Foothold: 78, RX0: -540, RX1: 3420, RespawnSeconds: -1}},
			{MapID: HoblinEscortSecond, Life: Life{Kind: "m", TemplateID: HoblinEscort, X: 138, Y: -747,
				Foothold: 89, RX0: -160, RX1: 2048, RespawnSeconds: -1}},
			{MapID: HoblinArena, Life: Life{Kind: "m", TemplateID: HoblinArenaEscort, X: -493, Y: 173,
				Foothold: 4, RX0: -607, RX1: 594, RespawnSeconds: -1}},
		},
	}
}

func usesEscortRoute(mapID int) bool {
	switch mapID {
	case IceKnightFirst, IceKnightSecond, HoblinEscortFirst, HoblinEscortSecond, HoblinArena:
		return true
	default:
		return false
	}
}

func HoblinSeal(root string) (Reactor, error) {
	reactors, err := readMapReactorDefinitions(root, HoblinArena)
	if err != nil {
		return Reactor{}, err
	}
	for _, reactor := range reactors {
		if reactor.TemplateID == 2118003 {
			return reactor, nil
		}
	}
	return Reactor{}, fmt.Errorf("seal 2118003 is missing from map %d", HoblinArena)
}
