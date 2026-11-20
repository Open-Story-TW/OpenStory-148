package content

const (
	OrbisPQName     = "OrbisPQ"
	OrbisPQLobby    = 200080101
	OrbisPQEntrance = 920010000
	OrbisPQHall     = 920010100
	OrbisPQWalkway  = 920010200
	OrbisPQStorage  = 920010300
	OrbisPQMusic    = 920010400
	OrbisPQAscent   = 920010700
	OrbisPQGarden   = 920010800
	OrbisPQHidden   = 920011000
	OrbisPQBonus    = 920011100
	OrbisPQExit     = 920011200
	OrbisPQResult   = 920011300

	OrbisCloudReactor = 2002001
	OrbisEakReactor   = 2006000
	OrbisCloudPiece   = 4001063
	OrbisPQSeconds    = 1200
)

func orbisPQEvent() Event {
	return Event{
		Name: OrbisPQName, LobbyMap: OrbisPQLobby, ExitMap: OrbisPQExit,
		Maps: []int{OrbisPQEntrance, OrbisPQHall, OrbisPQWalkway, OrbisPQStorage,
			OrbisPQMusic, OrbisPQAscent, OrbisPQGarden, OrbisPQHidden, OrbisPQBonus, OrbisPQResult,
			920010900, 920010910, 920010911, 920010912, 920010920, 920010921, 920010922,
			920010930, 920010931, 920010932},
		Seconds: OrbisPQSeconds, PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 70, LevelMax: 255,
	}
}

func OrbisPQReactor(mapID, template int) bool {
	switch mapID {
	case OrbisPQEntrance:
		return template == OrbisCloudReactor || template == OrbisEakReactor
	case OrbisPQHall:
		return template >= 2008000 && template <= 2008005 || template == 2006001
	case OrbisPQMusic:
		return template >= 2002004 && template <= 2002010 || template == 2008006
	case OrbisPQAscent:
		return template == 2008007
	case OrbisPQGarden:
		return template == 2001000 || template == 2001001
	default:
		return false
	}
}

func OrbisPuzzleDrop(mapID, itemID int) bool {
	if itemID < 4001044 || itemID > OrbisCloudPiece {
		return false
	}
	switch mapID {
	case OrbisPQEntrance, OrbisPQHall, OrbisPQWalkway, OrbisPQStorage,
		OrbisPQMusic, OrbisPQAscent, OrbisPQGarden, OrbisPQHidden:
		return true
	}
	return false
}
