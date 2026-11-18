package content

const (
	AmoriaName       = "Amoria"
	AmoriaApproach   = 670010000
	AmoriaLobby      = 670010100
	AmoriaMirror     = 670010200
	AmoriaRopes      = 670010300
	AmoriaPlatforms  = 670010400
	AmoriaCodes      = 670010500
	AmoriaGates      = 670010600
	AmoriaBalrog     = 670010700
	AmoriaBonus      = 670010800
	AmoriaExit       = 670011000
	AmoriaGatekeeper = 9201043
	AmoriaGuide      = 9201044
	AmoriaGuard      = 9201045
	AmoriaBonusNPC   = 9201046
	AmoriaGlitterman = 9201047
	AmoriaLeader     = 9201048
	AmoriaTicket     = 4031592
	AmoriaKey        = 4031593
	AmoriaFang       = 4031594
	AmoriaShard      = 4031595
	AmoriaHammer     = 4031596
	AmoriaCode       = 4031597
	AmoriaFairy      = 9400518
	AmoriaFirstForm  = 9400536
	AmoriaFinalForm  = 9400514
)

func amoriaEvent() Event {
	return Event{
		Name: AmoriaName, LobbyMap: AmoriaLobby, ExitMap: AmoriaExit,
		Maps:    []int{AmoriaMirror, AmoriaRopes, AmoriaPlatforms, AmoriaCodes, AmoriaGates, AmoriaBalrog, AmoriaBonus},
		Seconds: 75 * 60, PartyMin: 6, PartyMax: 6, Keep: 6, LevelMin: 40, LevelMax: 255,
	}
}

func AmoriaReactor(mapID, id int) bool {
	return mapID == AmoriaMirror && id == 6702000 ||
		mapID == AmoriaGates && id == 6702001 ||
		mapID == AmoriaBonus && id >= 6702003 && id <= 6702012
}

// AmoriaMonsterDrop keeps the shared sewer/purplewood monster templates
// from producing the party quest's items outside their original fields.
func AmoriaMonsterDrop(mapID, mobID, itemID int) (restricted, allowed bool) {
	switch itemID {
	case AmoriaKey:
		return true, (mapID == 670000100 || mapID == 670000200) && (mobID == 9400515 || mobID == 9400516)
	case AmoriaHammer:
		return true, mapID == AmoriaMirror && mobID == AmoriaFairy
	case AmoriaFang:
		return true, mapID == AmoriaBalrog && mobID == AmoriaFinalForm
	case AmoriaCode:
		return true, mapID == AmoriaCodes && (mobID == 9400530 || mobID == 9400531)
	}
	return false, false
}
