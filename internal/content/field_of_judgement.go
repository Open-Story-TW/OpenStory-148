package content

import (
	"fmt"
	"slices"
)

const (
	JudgementLobby = 101000000
	JudgementArena = 270050100
	JudgementNPC   = 9010003
	// These five room keys use the JudgementArena asset with separate
	// occupancy and combat state.
	JudgementRoomFirst = 990050101
	JudgementRoomCount = 5
)

func JudgementRoom(id int) bool {
	return id >= JudgementRoomFirst && id < JudgementRoomFirst+JudgementRoomCount
}

func JudgementRoomForLevel(level int) int {
	switch {
	case level < 21:
		return 0
	case level <= 30:
		return JudgementRoomFirst
	case level <= 50:
		return JudgementRoomFirst + 1
	case level <= 70:
		return JudgementRoomFirst + 2
	case level <= 90:
		return JudgementRoomFirst + 3
	default:
		return JudgementRoomFirst + 4
	}
}

type JudgementWave struct {
	TemplateID, Count, hpMultiple int
	hpOverride, expDivisor        int64
}

// Piggy2's five level brackets use the original v148 mob stats as their base.
var judgementWaves = [JudgementRoomCount][]JudgementWave{
	{{9500169, 100, 3, 0, 4}, {9300136, 10, 1, 15000, 1}},
	{{5120000, 100, 1, 30000, 4}, {9300012, 3, 1, 0, 1}},
	{{7130400, 33, 2, 0, 4}, {7130401, 33, 2, 0, 4}, {7130402, 34, 2, 0, 4}, {9300105, 3, 1, 0, 4}},
	{{7130010, 25, 2, 0, 4}, {7130001, 25, 2, 0, 4}, {9500134, 25, 2, 0, 4}, {9300039, 3, 1, 0, 1}},
	{{8141100, 30, 1, 0, 4}, {8140600, 30, 1, 0, 4}, {8150100, 30, 1, 0, 4}, {9300152, 3, 1, 2_000_000, 1}},
}

func JudgementWaves(room int) []JudgementWave {
	if !JudgementRoom(room) {
		return nil
	}
	return slices.Clone(judgementWaves[room-JudgementRoomFirst])
}

func (w JudgementWave) Stats(mob Mob) Mob {
	mob.HP *= int64(w.hpMultiple)
	if w.hpOverride > 0 {
		mob.HP = w.hpOverride
	}
	mob.EXP /= w.expDivisor
	return mob
}

func (c *Catalog) judgementMap(id int) (Map, error) {
	field, err := c.Map(JudgementArena)
	if err != nil {
		return Map{}, err
	}
	spawn, ok := field.Spawn()
	if !ok {
		return Map{}, fmt.Errorf("Field of Judgement arena has no spawn")
	}
	field.ID = id
	field.ReturnMap = JudgementLobby
	field.OnUserEnter = ""
	field.Portals = []Portal{spawn}
	field.Life = nil
	field.Areas = nil
	return field, nil
}
