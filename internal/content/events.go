package content

// Event describes one instanced activity (party quest, boss fight) that a
// single group holds on a channel at a time. Its stage maps are the channel's
// ordinary maps: while a group is inside nobody else may enter, as with the
// Legacy event manager's "state" property.
type Event struct {
	Name string
	// Maps are the stage maps; the group enters Maps[0] at EntryPortal.
	Maps        []int
	EntryPortal int
	// ExitMap receives everyone when the event ends, and anyone found on a
	// stage map without belonging to the running group.
	ExitMap int
	// Seconds is the time limit shown on the client's clock.
	Seconds int
	// PartyMin members must stand on the leader's map to start, and the event
	// ends as soon as fewer than Keep remain inside.
	PartyMin, PartyMax int
	Keep               int
	LevelMin, LevelMax int
	Spawns             []EventSpawn
}

// EventSpawn is a scripted spawn added each time the event's map is reset.
type EventSpawn struct {
	MapID int
	Life  Life
}

func (e Event) Stage(mapID int) bool {
	for _, id := range e.Maps {
		if id == mapID {
			return true
		}
	}
	return false
}

var events = map[string]Event{
	// 第一次同行 (Legacy event/KerningPQ.js): five stages and the bonus map.
	"KerningPQ": {
		Name: "KerningPQ", Maps: []int{910340100, 910340200, 910340300, 910340400, 910340500, 910340600},
		ExitMap: 910340000, Seconds: 1200, PartyMin: 3, PartyMax: 6, Keep: 2, LevelMin: 20, LevelMax: 255,
		// The final map has no WZ life entry for King Slime. Spawn it on
		// the lower platform between the two NPCs.
		Spawns: []EventSpawn{{MapID: 910340500, Life: Life{Kind: "m", TemplateID: 9300003,
			X: 35, Y: -435, Foothold: 53, RX0: 0, RX1: 90, RespawnSeconds: -1}}},
	},
}

var eventByMap = func() map[int]string {
	out := map[int]string{}
	for name, e := range events {
		for _, id := range e.Maps {
			out[id] = name
		}
	}
	return out
}()

func EventByName(name string) (Event, bool) {
	e, ok := events[name]
	return e, ok
}

// EventByMap finds the event that owns a stage map.
func EventByMap(mapID int) (Event, bool) {
	e, ok := events[eventByMap[mapID]]
	return e, ok
}
