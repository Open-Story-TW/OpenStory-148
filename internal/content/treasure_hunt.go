package content

const TreasureHuntLobby = 109010000

func TreasureHuntMap(mapID int) bool {
	return mapID == TreasureHuntLobby || mapID >= 109010100 && mapID <= 109010110 || mapID >= 109010200 && mapID <= 109010206
}

func TreasureHuntReactor(mapID, templateID int) bool {
	return TreasureHuntMap(mapID) && (templateID == 9001000 || templateID >= 9002000 && templateID <= 9002002)
}
