package content

const (
	EllinPQName   = "Ellin"
	EllinLobby    = 300030100
	EllinEntrance = 930000000
	EllinGlade    = 930000100
	EllinBoulder  = 930000200
	EllinMaze     = 930000300
	EllinCapture  = 930000400
	EllinBaskets  = 930000500
	EllinAltar    = 930000600
	EllinResult   = 930000700
	EllinExit     = 930000800

	EllinStoneShard     = 4001161
	EllinDilutedPoison  = 4001162
	EllinPurpleStone    = 4001163
	EllinMonsterMarble  = 4001169
	EllinAltairFragment = 4001198
	EllinPurifyMarble   = 2270004
	EllinPoisonMonster  = 9300175
	EllinPoisonGolem    = 9300180
	EllinClearForm      = 9300183
)

func ellinPQEvent() Event {
	return Event{
		Name: EllinPQName, ExitMap: EllinExit, Seconds: 1200,
		PartyMin: 3, PartyMax: 5, Keep: 3, LevelMin: 70, LevelMax: 255,
		Maps: []int{EllinEntrance, EllinGlade, EllinBoulder, EllinMaze, EllinCapture, EllinBaskets, EllinAltar},
	}
}

func EllinReactors(root string, mapID int) ([]Reactor, error) {
	definitions, err := readMapReactorDefinitions(root, mapID)
	if err != nil {
		return nil, err
	}
	var out []Reactor
	for _, def := range definitions {
		switch def.TemplateID {
		case 3001000, 3002000, 3002001, 3009000:
			out = append(out, def)
		}
	}
	return out, nil
}
