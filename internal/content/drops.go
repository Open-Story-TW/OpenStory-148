package content

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// DropDefinition retains unscaled source probabilities. Ordinary equip chances
// receive the legacy tenfold adjustment when a loot policy evaluates the row.
type DropDefinition struct {
	ID, MobID, TemplateID, Minimum, Maximum, QuestID, Chance int
	Global                                                   bool
	Continent, DropType                                      int
}

//go:embed mob_drops.json
var mobDropData []byte

var dropOnce sync.Once
var dropsByMob map[int][]DropDefinition
var globalDrops []DropDefinition
var dropError error

func loadDropDefinitions() {
	var bundle struct {
		Version int                 `json:"version"`
		Drops   [][]int             `json:"drops"`
		Global  [][]json.RawMessage `json:"global"`
	}
	if err := json.Unmarshal(mobDropData, &bundle); err != nil {
		dropError = err
		return
	}
	if bundle.Version != 1 {
		dropError = fmt.Errorf("unsupported drop catalog version")
		return
	}
	dropsByMob = make(map[int][]DropDefinition)
	for _, row := range bundle.Drops {
		if len(row) != 7 {
			dropError = fmt.Errorf("malformed monster drop row")
			return
		}
		d := DropDefinition{ID: row[0], MobID: row[1], TemplateID: row[2], Minimum: row[3], Maximum: row[4], QuestID: row[5], Chance: row[6]}
		if !validDrop(d) || d.MobID < 1 {
			dropError = fmt.Errorf("invalid monster drop %d", d.ID)
			return
		}
		dropsByMob[d.MobID] = append(dropsByMob[d.MobID], d)
	}
	for _, row := range bundle.Global {
		if len(row) != 9 {
			dropError = fmt.Errorf("malformed global drop row")
			return
		}
		fields := make([]int, 8)
		for i := range fields {
			if err := json.Unmarshal(row[i], &fields[i]); err != nil {
				dropError = err
				return
			}
		}
		d := DropDefinition{ID: fields[0], Continent: fields[1], DropType: fields[2], TemplateID: fields[3], Minimum: fields[4], Maximum: fields[5], QuestID: fields[6], Chance: fields[7], Global: true}
		if !validDrop(d) {
			dropError = fmt.Errorf("invalid global drop %d", d.ID)
			return
		}
		globalDrops = append(globalDrops, d)
	}
}

func validDrop(d DropDefinition) bool {
	return d.ID > 0 && d.TemplateID >= 0 && d.Minimum >= 0 && d.Maximum >= d.Minimum && d.QuestID >= 0 && d.Chance >= 0
}

// MonsterDrops includes applicable global rows. Global item 0 represents the
// legacy NX award, not a ground meso bag; consumers must handle it explicitly.
func MonsterDrops(mobID, mapID int) ([]DropDefinition, error) {
	dropOnce.Do(loadDropDefinitions)
	if dropError != nil {
		return nil, dropError
	}
	if mobID < 1 || mapID < 0 {
		return nil, fmt.Errorf("invalid drop lookup")
	}
	var out []DropDefinition
	seenAmoria := false
	for _, row := range dropsByMob[mobID] {
		if restricted, allowed := AmoriaMonsterDrop(mapID, mobID, row.TemplateID); restricted {
			if seenAmoria || !allowed {
				continue
			}
			seenAmoria = true
		}
		out = append(out, row)
	}
	if mapID == KentaBoss && (mobID == 9300461 || mobID == 9300468) {
		out = append(out, DropDefinition{ID: mobID, MobID: mobID, TemplateID: KentaScales, Minimum: 1, Maximum: 1, Chance: 1000000})
	}
	if EscapeKeyMob(mapID, mobID) {
		// The prison's four key guards provide the items for its four
		// physical doors. The same mob template outside this room has no key.
		out = append(out, DropDefinition{ID: -EscapeKey, MobID: mobID, TemplateID: EscapeKey, Minimum: 1, Maximum: 1, Chance: 999999})
	}
	for _, d := range globalDrops {
		if d.Continent < 0 || d.Continent < 10 && mapID/100000000 == d.Continent || d.Continent < 100 && mapID/10000000 == d.Continent || d.Continent < 1000 && mapID/1000000 == d.Continent {
			out = append(out, d)
		}
	}
	return out, nil
}
