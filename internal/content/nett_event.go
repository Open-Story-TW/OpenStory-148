package content

const (
	NettNormal = "NettNormal"
	NettHard   = "NettHard"
)

func NettEvent(name string) bool { return name == NettNormal || name == NettHard }

func nettEvent(name string, room, minLevel, maxLevel int) Event {
	return Event{
		Name: name, LobbyMap: NettLobby, ExitMap: NettDefenseExit,
		Maps: []int{room}, RequiredMobs: map[int]map[int]int{room: {}},
		PartyMin: NettLanes, PartyMax: NettLanes, Keep: NettLanes,
		LevelMin: minLevel, LevelMax: maxLevel, Seconds: 1800,
	}
}
