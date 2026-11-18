package content

import (
	"fmt"
	"strconv"
)

const (
	CarnivalInvitationQuest = 7057
	CarnivalNPC             = 2042000
	CarnivalWaitingMinimum  = 6
	CarnivalTeamSize        = 3
	CarnivalMinimumLevel    = 80
	CarnivalWaitingFirst    = 980090000
	CarnivalWaitingLast     = 980090001
	CarnivalArenaFirst      = 980000101
	CarnivalArenaLast       = 980002001
	CarnivalExit            = 980000010
	CarnivalSecondLobby     = 980030000
	CarnivalSecondExit      = 980030010
)

type CarnivalSummon struct {
	MobID          int
	Cost           int
	Count          int
	RespawnSeconds int
}

type CarnivalField struct {
	TimeLimit             int
	TimeExtra             int
	MessageDelay          int
	ResultDelay           int
	WinnerMap             int
	LoserMap              int
	DeathCost             int
	MaxSummoned           int
	Summons               []CarnivalSummon
	SpawnPoints           []Life
	GuardianPoints        []Life
	SkillSlots            []int
	GuardianSlots         []int
	WinEffect, LoseEffect string
	WinSound, LoseSound   string
}

func CarnivalWaitingRoom(mapID int) bool {
	return mapID == 980090000 || mapID == 980090001
}

// CarnivalMatchField includes the v148 waiting, combat, revival and results maps.
func CarnivalMatchField(mapID int) bool {
	return CarnivalWaitingRoom(mapID) ||
		mapID >= CarnivalArenaFirst && mapID <= CarnivalArenaLast+3 && mapID%100 >= 1 && mapID%100 <= 4
}

// CarnivalSecondField recognizes the retired mode's rooms, including its linked
// copies. They are retained in the client archive with the quest disabled.
func CarnivalSecondField(mapID int) bool {
	return mapID == CarnivalSecondLobby || mapID == CarnivalSecondExit ||
		mapID >= 980031000 && mapID < 980034000
}

// CarnivalArenaMode considers both live v148 type 10 rooms and the
// retained type 15 arenas. It does not grant admission to either set.
func CarnivalArenaMode(mapID int) int {
	if mapID >= CarnivalArenaFirst && mapID <= CarnivalArenaLast && mapID%100 == 1 {
		return 1
	}
	switch mapID {
	case 980031100, 980032100, 980033100:
		return 2
	}
	return 0
}

func parseCarnivalField(m *Map, src *wzNode) error {
	mode := CarnivalArenaMode(m.ID)
	if mode == 0 {
		return nil
	}
	cg := src.child("monsterCarnival")
	if cg == nil {
		// Some catalog mounts select only map geometry. They can still resolve
		// the field, while admission requires the complete arena settings.
		return nil
	}
	field := &CarnivalField{
		TimeLimit: cg.intValue("timeDefault", 0), TimeExtra: cg.intValue("timeExpand", 0),
		MessageDelay: cg.intValue("timeMessage", 0), ResultDelay: cg.intValue("timeFinish", 0), WinnerMap: cg.intValue("rewardMapWin", 0),
		LoserMap: cg.intValue("rewardMapLose", 0), DeathCost: cg.intValue("deathCP", 0),
		MaxSummoned: cg.intValue("mobGenMax", 0),
		WinEffect:   cg.stringValue("effectWin", ""), LoseEffect: cg.stringValue("effectLose", ""),
		WinSound: cg.stringValue("soundWin", ""), LoseSound: cg.stringValue("soundLose", ""),
	}
	if field.TimeLimit < 1 || field.TimeLimit > 3600 || field.TimeExtra < 0 || field.TimeExtra > 1800 ||
		field.ResultDelay < 0 || field.ResultDelay > 60 || field.MessageDelay < 0 || field.MessageDelay > field.ResultDelay ||
		field.DeathCost < 0 || field.DeathCost > 255 || field.MaxSummoned < 1 || field.MaxSummoned > 64 ||
		field.WinnerMap <= 0 || field.LoserMap <= 0 || field.WinnerMap == field.LoserMap {
		return fmt.Errorf("carnival map %d has invalid arena settings", m.ID)
	}
	if summons := cg.child("mob"); summons != nil {
		for _, row := range summons.Children {
			count := row.intValue("mobCount", 1)
			option := CarnivalSummon{MobID: row.intValue("id", 0), Cost: row.intValue("spendCP", 0),
				Count: count, RespawnSeconds: row.intValue("mobTime", -1)}
			if option.MobID <= 0 || option.Cost <= 0 || option.Count <= 0 || option.Count > field.MaxSummoned {
				return fmt.Errorf("carnival map %d has invalid summon %s", m.ID, row.Name)
			}
			field.Summons = append(field.Summons, option)
		}
	}
	field.SpawnPoints = carnivalPositions(cg.child("mobGenPos"))
	field.GuardianPoints = carnivalPositions(cg.child("guardianGenPos"))
	field.SkillSlots = carnivalSlots(cg.child("skill"))
	field.GuardianSlots = carnivalSlots(cg.child("guardian"))
	if len(field.SpawnPoints) == 0 || len(field.Summons) == 0 {
		return fmt.Errorf("carnival map %d has no monster spawn definitions", m.ID)
	}
	m.Carnival = field
	return nil
}

func carnivalPositions(list *wzNode) []Life {
	var positions []Life
	if list == nil {
		return positions
	}
	for _, entry := range list.Children {
		x, y := entry.intValue("x", 0), entry.intValue("y", 0)
		positions = append(positions, Life{Kind: "m", X: x, Y: entry.intValue("cy", y),
			Foothold: entry.intValue("fh", 0), RX0: x - 70, RX1: x + 70})
	}
	return positions
}

func carnivalSlots(list *wzNode) []int {
	var slots []int
	if list == nil {
		return slots
	}
	for _, row := range list.Children {
		id, err := strconv.Atoi(row.Value)
		if err == nil && id >= 0 {
			slots = append(slots, id)
		}
	}
	return slots
}
