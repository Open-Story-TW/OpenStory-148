package content

const (
	DragonRiderName  = "Dragonica"
	DragonRiderGate  = 240080000
	DragonRiderSky   = 240080600
	DragonRiderTrap  = 240080700
	DragonRiderArena = 240080800
	DragonRiderExit  = 240080050
	DragonRiderBoss  = 8300007
)

func dragonRiderEvent() Event {
	return Event{
		Name: DragonRiderName, ExitMap: DragonRiderGate, Seconds: 1800,
		PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 120, LevelMax: 255,
		Maps: []int{DragonRiderSky, DragonRiderTrap, DragonRiderArena},
		// Legacy Dragonica spawns the boss on the arena's foothold 15.
		Spawns: []EventSpawn{{MapID: DragonRiderArena, Life: Life{Kind: "m", TemplateID: DragonRiderBoss,
			X: 700, Y: -10, Foothold: 15, RX0: 675, RX1: 765, RespawnSeconds: -1}}},
	}
}
