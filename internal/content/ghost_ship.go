package content

import (
	"fmt"
	"strconv"
)

const (
	GhostLobby     = 923020000
	GhostExit      = 923020001
	GhostWinner    = 923020010
	GhostLoser     = 923020020
	GhostRegistrar = 2060103
	GhostBoss      = 9700037
	GhostFragment  = 4001533
	GhostTeams     = 2
	GhostTeamSize  = 3
	GhostRooms     = 5
	GhostStages    = 5
	GhostLevelMin  = 120
	GhostLevelMax  = 159
)

// GhostRoom identifies the five original TWMS v148 pairs of linked courses.
// Both parties use one boss map after racing through distinct route maps.
type GhostRoom int

func (r GhostRoom) Valid() bool {
	return r >= 1 && r <= GhostRooms
}

func (r GhostRoom) Waiting() int    { return GhostLobby + int(r)*100 }
func (r GhostRoom) Revive() int     { return r.Waiting() + 9 }
func (r GhostRoom) BossRevive() int { return r.Waiting() + 89 }
func (r GhostRoom) Boss() int       { return r.Waiting() + 90 }

func (r GhostRoom) Course(team, stage int) int {
	return r.Waiting() + 10*(team+1) + stage
}

func (r GhostRoom) Event() Event {
	maps := []int{r.Waiting(), r.Revive(), r.BossRevive(), r.Boss()}
	for team := range GhostTeams {
		for stage := range GhostStages {
			maps = append(maps, r.Course(team, stage))
		}
	}
	return Event{Name: "GhostShip" + strconv.Itoa(int(r)), Maps: maps, ExitMap: GhostExit, Seconds: 30 * 60,
		PartyMin: GhostTeamSize, PartyMax: GhostTeamSize, Keep: GhostTeamSize, LevelMin: GhostLevelMin, LevelMax: GhostLevelMax}
}

func GhostRoomOf(mapID int) (GhostRoom, bool) {
	r := GhostRoom((mapID - GhostLobby) / 100)
	if !r.Valid() {
		return 0, false
	}
	switch suffix := mapID - r.Waiting(); {
	case suffix == 0, suffix == 9, suffix == 89, suffix == 90,
		suffix >= 10 && suffix <= 14, suffix >= 20 && suffix <= 24:
		return r, true
	default:
		return 0, false
	}
}

// GhostDeckReactors reads the original pair of switches and the spring from
// the shared stage-five map image used by both teams and all room numbers.
func GhostDeckReactors(root string) ([3]Reactor, error) {
	definitions, err := readMapReactorDefinitions(root, GhostRoom(1).Course(0, GhostStages-1))
	var out [3]Reactor
	if err != nil {
		return out, err
	}
	templates := [...]int{2309000, 2309000, 2308000}
	for _, reactor := range definitions {
		if reactor.Index >= 0 && reactor.Index < len(out) && reactor.TemplateID == templates[reactor.Index] {
			out[reactor.Index] = reactor
		}
	}
	for i, reactor := range out {
		if reactor.TemplateID == 0 {
			return out, fmt.Errorf("missing Ghost Ship deck reactor %d", i)
		}
	}
	return out, nil
}
