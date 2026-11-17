package content

const (
	PiratePQName    = "Pirate"
	PiratePQLobby   = 251010404
	PiratePQShore   = 925100000
	PiratePQMedals  = 925100100
	PiratePQLower   = 925100200
	PiratePQUpper   = 925100300
	PiratePQDoors   = 925100400
	PiratePQCaptain = 925100500
	PiratePQResult  = 925100600
	PiratePQExit    = 925100700
	PiratePQKey     = 4001117
	PiratePQBadge   = 4001455
)

// Quest 1204 describes four timed sections of the six-map route. The two
// guard decks share a section and the door deck shares its clock with the boss.
func PiratePQSection(mapID int) (index, seconds int) {
	switch {
	case mapID == PiratePQShore:
		return 1, 240
	case mapID == PiratePQMedals:
		return 2, 420
	case mapID >= PiratePQLower && mapID <= PiratePQUpper+2:
		return 3, 300
	case mapID == PiratePQDoors || mapID == PiratePQCaptain:
		return 4, 360
	default:
		return 0, 0
	}
}

func piratePQEvent() Event {
	return Event{
		Name: PiratePQName, ExitMap: PiratePQExit, Seconds: 1320,
		PartyMin: 3, PartyMax: 6, Keep: 3, LevelMin: 70, LevelMax: 255,
		Maps: []int{PiratePQShore, PiratePQMedals,
			PiratePQLower, PiratePQLower + 1, PiratePQLower + 2,
			PiratePQUpper, PiratePQUpper + 1, PiratePQUpper + 2,
			PiratePQDoors, PiratePQCaptain},
	}
}

// PirateReactorItem is the drop condition from the TWMS v148 reactor WZ.
func PirateReactorItem(reactor int) int {
	switch reactor {
	case 2519000, 2519001, 2519002, 2519003:
		return PiratePQKey
	case 2512001:
		return 4031437
	default:
		return 0
	}
}

func PiratePQReactors(root string, mapID int) ([]Reactor, error) {
	all, err := readMapReactorDefinitions(root, mapID)
	if err != nil {
		return nil, err
	}
	var reactors []Reactor
	for _, def := range all {
		switch def.TemplateID {
		case 2511000, 2511001, 2512001, 2518000, 2519000, 2519001, 2519002, 2519003:
			reactors = append(reactors, def)
		}
	}
	return reactors, nil
}
