package content

import (
	"fmt"
	"path/filepath"
	"time"
)

// Recovery contains only immediate HP/MP restoration. Items with any other
// effect must use that effect's validated gameplay implementation instead.
type Recovery struct{ HP, MP, HPRate, MPRate int }

// ItemBuff is the timed stat part of a USE item: WZ spec keys (pad, mad, pdd,
// mdd, acc, eva, speed, jump) and their values, lasting Duration.
type ItemBuff struct {
	Duration time.Duration
	Values   map[string]int
}

// itemBuffKeys are the stat keys a consumable buff may carry. Any other key
// (morph, drop rate, map limits, ...) keeps the item rejected.
var itemBuffKeys = map[string]bool{"pad": true, "mad": true, "pdd": true, "mdd": true, "acc": true, "eva": true, "speed": true, "jump": true, "inflation": true}

// RecoveryDefinition is the immediate HP/MP restoration of an item without a
// timed effect. An item with a buff is rejected; ConsumableDefinition reads both.
func RecoveryDefinition(root string, id int) (Recovery, error) {
	r, buff, err := ConsumableDefinition(root, id)
	if err != nil {
		return Recovery{}, err
	}
	if buff != nil {
		return Recovery{}, fmt.Errorf("item %d has a timed effect", id)
	}
	return r, nil
}

// ConsumableDefinition reads a USE item's immediate recovery and its timed
// stat buff (nil when it has none).
func ConsumableDefinition(root string, id int) (Recovery, *ItemBuff, error) {
	if root == "" {
		switch id {
		case 2000000:
			return Recovery{HP: 50}, nil, nil
		case 2010000, 2010007:
			return Recovery{HP: 30}, nil, nil
		case 2010009:
			return Recovery{MP: 30}, nil, nil
		case 2002000: // Dexterity Pill: avoidability +100 for 180 s.
			return Recovery{}, &ItemBuff{Duration: 180 * time.Second, Values: map[string]int{"eva": 100}}, nil
		case 2003516: // Giant Potion: inflation 300, speed +20, jump +20 for 60 s.
			return Recovery{}, &ItemBuff{Duration: 60 * time.Second, Values: map[string]int{"inflation": 300, "speed": 20, "jump": 20}}, nil
		default:
			return Recovery{}, nil, fmt.Errorf("recovery item %d is absent from embedded content", id)
		}
	}
	if id < 2000000 || id >= 3000000 {
		return Recovery{}, nil, fmt.Errorf("not a USE recovery item")
	}
	bundle, err := readWZ(filepath.Join(root, "Item.wz", "Consume", fmt.Sprintf("%04d.img.xml", id/10000)))
	if err != nil {
		return Recovery{}, nil, err
	}
	node := bundle.child(fmt.Sprintf("%08d", id))
	if node == nil {
		return Recovery{}, nil, fmt.Errorf("item %d missing", id)
	}
	if node.child("specEx") != nil {
		return Recovery{}, nil, fmt.Errorf("item %d has additional effects", id)
	}
	spec := node.child("spec")
	if spec == nil {
		return Recovery{}, nil, fmt.Errorf("item %d has no recovery spec", id)
	}
	values := map[string]int{}
	for _, entry := range spec.Children {
		switch {
		case entry.Name == "hp", entry.Name == "mp", entry.Name == "hpR", entry.Name == "mpR", entry.Name == "time":
		case entry.Name == "morph" && id == HorntailDisguiseItem:
			if entry.Value != "4" {
				return Recovery{}, nil, fmt.Errorf("invalid Horntail disguise")
			}
			values["morph"] = 4
		case itemBuffKeys[entry.Name]:
			if v := spec.intValue(entry.Name, 0); v != 0 {
				values[entry.Name] = v
			}
		default:
			return Recovery{}, nil, fmt.Errorf("item %d has unsupported effect %s", id, entry.Name)
		}
	}
	result := Recovery{HP: spec.intValue("hp", 0), MP: spec.intValue("mp", 0), HPRate: spec.intValue("hpR", 0), MPRate: spec.intValue("mpR", 0)}
	if result.HP < 0 || result.MP < 0 || result.HPRate < 0 || result.HPRate > 100 || result.MPRate < 0 || result.MPRate > 100 {
		return Recovery{}, nil, fmt.Errorf("invalid recovery item %d", id)
	}
	var buff *ItemBuff
	ms := spec.intValue("time", 0)
	if len(values) > 0 {
		// The client takes an I32 millisecond duration; a buff shorter than a
		// second is not one Java gives.
		if ms < 1000 || ms > 2100000000 {
			return Recovery{}, nil, fmt.Errorf("item %d buff time %d invalid", id, ms)
		}
		for _, v := range values {
			if v < -32768 || v > 32767 {
				return Recovery{}, nil, fmt.Errorf("item %d buff value %d invalid", id, v)
			}
		}
		buff = &ItemBuff{Duration: time.Duration(ms) * time.Millisecond, Values: values}
	} else if spec.child("time") != nil {
		return Recovery{}, nil, fmt.Errorf("item %d has a time without a supported stat", id)
	}
	if result == (Recovery{}) && buff == nil {
		return Recovery{}, nil, fmt.Errorf("invalid recovery item %d", id)
	}
	return result, buff, nil
}
