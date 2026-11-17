package content

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

const (
	NettLobby       = 926010000
	NettNormalBase  = 926010200
	NettHardBase    = 926010300
	NettDefenseExit = 926020001
	NettNPC         = 2103013
	NettEmerald     = 4001623
	NettWaves       = 20
	NettLanes       = 4
)

type NettLane struct {
	X, Y int
}

type NettSpawn struct {
	MobID int
	Lane  int
	After time.Duration
}

type NettDefense struct {
	Lanes    [NettLanes]NettLane
	Waves    [NettWaves][]NettSpawn
	ObeliskX int
	MPBonus  int
	MPEvery  time.Duration
}

func NettDefenseMap(id int) bool {
	return id >= 926010100 && id <= 926010104 ||
		id >= NettNormalBase && id <= NettNormalBase+4 ||
		id >= NettHardBase && id <= NettHardBase+4
}

func parseNettDefense(root, info *wzNode) (*NettDefense, error) {
	lanes, waves := root.child("mobGenPos"), root.child("wave")
	if info.intValue("fieldType", 0) != 38 || lanes == nil || waves == nil ||
		len(lanes.Children) != NettLanes || len(waves.Children) != NettWaves {
		return nil, fmt.Errorf("invalid Nett defense field")
	}
	defense := &NettDefense{
		MPBonus: info.intValue("incMPr", 0),
		MPEvery: time.Duration(info.intValue("incInterval", 0)) * time.Millisecond,
	}
	for i := range defense.Lanes {
		lane := lanes.child(strconv.Itoa(i))
		if lane.child("x") == nil || lane.child("y") == nil {
			return nil, fmt.Errorf("missing Nett lane %d", i)
		}
		defense.Lanes[i] = NettLane{X: lane.intValue("x", 0), Y: lane.intValue("y", 0)}
	}
	for i := range defense.Waves {
		wave := waves.child(strconv.Itoa(i + 1))
		if wave == nil || len(wave.Children) == 0 {
			return nil, fmt.Errorf("missing Nett wave %d", i+1)
		}
		for _, row := range wave.Children {
			mobID, lane, delay := row.intValue("mobID", 0), row.intValue("genPos", -1), row.intValue("waveAfter", -1)
			if mobID < 1 || mobID > 9999999 || lane < 0 || lane >= NettLanes || delay < 0 {
				return nil, fmt.Errorf("invalid Nett spawn in wave %d", i+1)
			}
			defense.Waves[i] = append(defense.Waves[i], NettSpawn{MobID: mobID, Lane: lane, After: time.Duration(delay) * time.Millisecond})
		}
		sort.SliceStable(defense.Waves[i], func(a, b int) bool {
			return defense.Waves[i][a].After < defense.Waves[i][b].After
		})
	}
	return defense, nil
}
