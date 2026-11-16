package content

const (
	LudibriumPQName = "LudiPQ"
	LudiPQLobby     = 221023300
	LudiPQExit      = 922010000
	LudiPQClear     = 922011100
	LudiPQFirst     = 922010100
	LudiPQDark      = 922010400
	LudiPQBoxes     = 922010600
	LudiPQGolems    = 922010700
	LudiPQQuiz      = 922010800
	LudiPQBoss      = 922010900
	LudiPQPuppet    = 4001454
)

// TWMS v148 QuestInfo 1202 uses the six-stage revision. The traps and towers
// at 922010200, 922010300 and 922010500 belong to the older route.
func ludibriumEvent() Event {
	return Event{
		Name: LudibriumPQName, ExitMap: LudiPQExit, Seconds: 1200,
		PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 50, LevelMax: 255,
		Maps: []int{LudiPQFirst, LudiPQDark, 922010401, 922010402, 922010403, 922010404, 922010405,
			LudiPQBoxes, LudiPQGolems, LudiPQQuiz, LudiPQBoss},
		// Legacy lpq8 spawns Alishar at (1096, 184). v148 WZ foothold 90
		// supports that point, and mob 9300012 supplies the combat stats.
		Spawns: []EventSpawn{{MapID: LudiPQBoss, Life: Life{Kind: "m", TemplateID: 9300012, X: 1096, Y: 183,
			Foothold: 90, RX0: 960, RX1: 1200, RespawnSeconds: -1}}},
	}
}
