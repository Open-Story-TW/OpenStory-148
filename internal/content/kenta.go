package content

const (
	KentaPQName  = "KentaPQ"
	KentaLobby   = 923040000
	KentaRescue  = 923040100
	KentaBubbles = 923040200
	KentaDefense = 923040300
	KentaBoss    = 923040400
	KentaBubble  = 2430364
	KentaScales  = 4001535
	KentaEscort  = 9300460
)

func kentaEvent() Event {
	return Event{
		Name: KentaPQName, LobbyMap: KentaLobby, ExitMap: KentaLobby,
		Maps:    []int{KentaRescue, KentaBubbles, KentaDefense, KentaBoss},
		Seconds: 1200, PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 120, LevelMax: 255,
		RequiredMobs: map[int]map[int]int{KentaRescue: {9300444: 8, 9300445: 10}},
		Spawns: []EventSpawn{
			{MapID: KentaDefense, Life: Life{Kind: "m", TemplateID: KentaEscort, X: 68, Y: 598, RespawnSeconds: -1}},
			{MapID: KentaDefense, Life: Life{Kind: "n", TemplateID: 9020004, X: 68, Y: 598, RX0: 68, RX1: 68}},
			{MapID: KentaBoss, Life: Life{Kind: "n", TemplateID: 9020004, X: -161, Y: 123, RX0: -161, RX1: -161}},
			{MapID: KentaBoss, Life: Life{Kind: "m", TemplateID: 9300461, X: 400, Y: 123, RespawnSeconds: -1}},
			{MapID: KentaBoss, Life: Life{Kind: "m", TemplateID: 9300468, X: -1000, Y: 123, RespawnSeconds: -1}},
		},
	}
}
