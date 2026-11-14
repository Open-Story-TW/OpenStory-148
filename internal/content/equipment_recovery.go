package content

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"maple-go-server/internal/domain"
)

// Native equipment-info facts, distinct from option RecoveryUP and from item
// recoveryHP/MP overrides. Values are cached per absolute file and returned by
// value; no instance metadata, inventory state or mutable XML nodes escape.
type topRecoveryDefinition struct {
	multiplier float64
	cash       bool
}
type topRecoveryCache struct {
	once  sync.Once
	facts topRecoveryDefinition
	err   error
}

var topRecoveryDefinitions sync.Map

func loadTopRecoveryDefinition(root string, id int) (topRecoveryDefinition, error) {
	category, slots := equipmentCategory(id / 10000)
	if id < 1000000 || len(slots) != 1 || slots[0] != -5 || category != "Coat" && category != "Longcoat" {
		return topRecoveryDefinition{}, fmt.Errorf("invalid recovery top template %d", id)
	}
	if root == "" {
		def, ok := starterItem(id)
		if !ok {
			return topRecoveryDefinition{}, fmt.Errorf("unknown embedded recovery top %d", id)
		}
		return topRecoveryDefinition{multiplier: 1, cash: def.Cash}, nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return topRecoveryDefinition{}, err
	}
	path := filepath.Join(abs, "Character.wz", category, fmt.Sprintf("%08d.img.xml", id))
	entry, _ := topRecoveryDefinitions.LoadOrStore(path, &topRecoveryCache{})
	c := entry.(*topRecoveryCache)
	c.once.Do(func() {
		node, err := readWZ(path)
		if err != nil {
			c.err = err
			return
		}
		info := node.child("info")
		if info == nil {
			c.err = fmt.Errorf("recovery top has no info")
			return
		}
		c.facts.multiplier = 1
		// Classify template cash before parsing any multiplier. A cosmetic
		// template must never become a combat source, even with invalid recovery.
		cashSeen := false
		for _, leaf := range info.Children {
			if leaf.Name != "cash" {
				continue
			}
			value, err := strconv.Atoi(leaf.Value)
			if cashSeen || err != nil {
				c.err = fmt.Errorf("invalid recovery top cash flag")
				return
			}
			cashSeen, c.facts.cash = true, value != 0
		}
		if c.facts.cash {
			return
		}
		seen := map[string]bool{}
		for _, leaf := range info.Children {
			if leaf.Name != "recovery" {
				continue
			}
			if seen[leaf.Name] {
				c.err = fmt.Errorf("duplicate recovery top property %s", leaf.Name)
				return
			}
			seen[leaf.Name] = true
			value, err := strconv.ParseFloat(leaf.Value, 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				c.err = fmt.Errorf("invalid recovery top multiplier")
				return
			}
			c.facts.multiplier = value
		}
	})
	return c.facts, c.err
}

// ordinaryRecoveryRate follows9156F2..91572D: the *original* map rate must be
// strictly greater than1 before considering physical worn slot-5. Cash overlay,
// bag and expired assets cannot become a recovery source. Read outside scene
// publication/registry locks. Missing/invalid mounted content is a checked error,
// not an invented multiplier; an absent property on valid info defaults to1.
func ordinaryRecoveryRate(root string, c domain.Character, now time.Time, mapRate float64) (float64, error) {
	if mapRate <= 1 {
		return mapRate, nil
	}
	rate := mapRate
	seen := false
	for _, e := range c.Equipment {
		if e.Slot != -5 || e.CashID != 0 || e.ExpiresAt > 0 && e.ExpiresAt <= now.UnixMilli() {
			continue
		}
		def, err := loadTopRecoveryDefinition(root, e.ItemID)
		if err != nil {
			return 0, err
		}
		if def.cash {
			continue
		}
		if seen {
			return 0, fmt.Errorf("duplicate active recovery top")
		}
		seen = true
		rate = mapRate * def.multiplier
	}
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || 10*rate > 32767 || 3*rate > 32767 {
		return 0, fmt.Errorf("ordinary recovery amount out of native range")
	}
	return rate, nil
}
