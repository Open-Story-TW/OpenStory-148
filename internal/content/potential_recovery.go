package content

import (
	"fmt"
	"maple-go-server/internal/domain"
	"math"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// RecoveryPolicy describes one native requested recovery event. It never
// modifies HP/MP and never schedules a second server recovery timer.
type RecoveryPolicy struct {
	HP, MP   int
	Interval time.Duration
}
type recoveryMapFacts struct {
	rate      float64
	fieldType int
	footholds []Foothold
}
type recoveryMapCache struct {
	once  sync.Once
	facts recoveryMapFacts
	err   error
}

var recoveryMaps sync.Map
var embeddedRecoveryOnce sync.Once
var embeddedRecoveryCatalog *Catalog
var embeddedRecoveryError error

// ScaleReviewedRecovery applies PlayerStats.relocHeal's final map multiplier
// once, after base, skill bonus and effective INT. It is not used by native
// potential 0x92, whose RecoveryHP/MP amounts remain flat. Prepare this policy
// outside scene/publication and registry locks, just like ordinary map facts.
func ScaleReviewedRecovery(root string, mapID, hp, mp int) (int, int, error) {
	facts, err := loadRecoveryMap(root, mapID)
	if err != nil {
		return 0, 0, err
	}
	if facts.fieldType == 36 || hp < 0 || mp < 0 {
		return 0, 0, fmt.Errorf("unsupported periodic recovery state")
	}
	// Java3435 deliberately leaves the assembled4s source unchanged when rate
	// is zero. Native0x91 instead multiplies its10/3 base by zero; do not share
	// this exception with RequestedRecoveryPolicy or the flat potential source.
	rate := facts.rate
	if rate == 0 {
		rate = 1
	}
	h, m := float64(hp)*rate, float64(mp)*rate
	if h > math.MaxInt32 || m > math.MaxInt32 {
		return 0, 0, fmt.Errorf("periodic recovery amount out of range")
	}
	return int(h), int(m), nil
}

func loadRecoveryMap(root string, id int) (recoveryMapFacts, error) {
	if id < 0 || id > 999999998 {
		return recoveryMapFacts{}, fmt.Errorf("invalid recovery field")
	}
	if root == "" {
		// All23 embedded starter maps have absent info/recovery and fieldType;
		// the original mounted-WZ regression below checks this provenance.
		embeddedRecoveryOnce.Do(func() { embeddedRecoveryCatalog, embeddedRecoveryError = NewCatalog("") })
		if embeddedRecoveryError != nil {
			return recoveryMapFacts{}, embeddedRecoveryError
		}
		c := embeddedRecoveryCatalog
		if _, ok := c.maps[id]; !ok {
			return recoveryMapFacts{}, fmt.Errorf("unknown embedded recovery field")
		}
		return recoveryMapFacts{rate: 1, footholds: c.maps[id].Footholds}, nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return recoveryMapFacts{}, err
	}
	path := filepath.Join(abs, "Map.wz", "Map", fmt.Sprintf("Map%d", id/100000000), fmt.Sprintf("%09d.img.xml", id))
	entry, _ := recoveryMaps.LoadOrStore(path, &recoveryMapCache{})
	c := entry.(*recoveryMapCache)
	c.once.Do(func() {
		node, err := readWZ(path)
		if err != nil {
			c.err = err
			return
		}
		info := node.child("info")
		if info == nil {
			c.err = fmt.Errorf("recovery field has no info")
			return
		}
		rate, err := strconv.ParseFloat(info.stringValue("recovery", "1"), 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 3276.7 {
			c.err = fmt.Errorf("unsupported recovery rate")
			return
		}
		fieldType := 0
		if leaf := info.child("fieldType"); leaf != nil {
			value, e := strconv.Atoi(leaf.Value)
			if e != nil || value < 0 {
				c.err = fmt.Errorf("invalid recovery field type")
				return
			}
			fieldType = value
		}
		c.facts = recoveryMapFacts{rate: rate, fieldType: fieldType}
		if f := node.child("foothold"); f != nil {
			for _, layer := range f.Children {
				for _, group := range layer.Children {
					for _, leaf := range group.Children {
						id, e := strconv.Atoi(leaf.Name)
						if e != nil || id <= 0 {
							c.err = fmt.Errorf("invalid recovery foothold")
							return
						}
						c.facts.footholds = append(c.facts.footholds, Foothold{ID: id, X1: leaf.intValue("x1", 0), Y1: leaf.intValue("y1", 0), X2: leaf.intValue("x2", 0), Y2: leaf.intValue("y2", 0)})
					}
				}
			}
		}
	})
	return c.facts, c.err
}

// RequestedRecoveryPolicy follows9156B3/86F3FD. Natural standing recovery
// multiplies10/3 by map rate and truncates once; potential recovery is a separate
// flat sum and4s source, not multiplied by map rate or RecoveryUP. Sitting and
// booster items need additional actual native-state proof and remain unsupported.
func RequestedRecoveryPolicy(root string, c domain.Character, now time.Time, potential bool, mode byte) (RecoveryPolicy, error) {
	if c.HP <= 0 || mode != 0 {
		return RecoveryPolicy{}, fmt.Errorf("unsupported recovery state or chair")
	}
	facts, err := loadRecoveryMap(root, c.MapID)
	if err != nil {
		return RecoveryPolicy{}, err
	}
	if facts.fieldType == 36 {
		return RecoveryPolicy{}, fmt.Errorf("special field recovery is not implemented")
	}
	p := RecoveryPolicy{Interval: 10 * time.Second}
	if !potential {
		rate, err := ordinaryRecoveryRate(root, c, now, facts.rate)
		if err != nil {
			return RecoveryPolicy{}, err
		}
		p.HP, p.MP = int(10*rate), int(3*rate)
	}
	if potential {
		effects, err := ResolvePotentialEffects(root, c, now)
		if err != nil {
			return RecoveryPolicy{}, err
		}
		// Other unresolved options are not interpreted as healing; all-skill-level
		// effects can change maxima indirectly and remain unsafe even for this event.
		for _, line := range effects.Pending {
			for _, key := range line.Attributes {
				if key == "incAllskill" {
					return RecoveryPolicy{}, &PendingPotentialEffectsError{Effects: effects.Pending}
				}
			}
		}
		p = RecoveryPolicy{HP: effects.Values["RecoveryHP"], MP: effects.Values["RecoveryMP"], Interval: 4 * time.Second}
		if p.HP < 0 || p.MP < 0 || p.HP > 32767 || p.MP > 32767 {
			return RecoveryPolicy{}, fmt.Errorf("unsupported native recovery amount")
		}
	}
	if _, noMana := DemonFuryMax(c.Job); noMana {
		p.MP = 0
	}
	return p, nil
}

// RecoveryGroundSupport is a pure static-map admission predicate. It proves
// feet lie within1px of a nonvertical WZ foothold, not the native current FH.
// A known command14 FH must also match; command0's opaque short is never used.
func RecoveryGroundSupport(root string, id int) (func(x, y, foothold int) bool, error) {
	facts, err := loadRecoveryMap(root, id)
	if err != nil {
		return nil, err
	}
	if len(facts.footholds) == 0 {
		return nil, fmt.Errorf("recovery ground geometry unavailable")
	}
	return func(x, y, fh int) bool {
		for _, f := range facts.footholds {
			if f.X1 == f.X2 || fh != 0 && fh != f.ID || x < min(f.X1, f.X2) || x > max(f.X1, f.X2) {
				continue
			}
			expected := float64(f.Y1) + (float64(x)-float64(f.X1))/(float64(f.X2)-float64(f.X1))*(float64(f.Y2)-float64(f.Y1))
			if math.Abs(float64(y)-expected) <= 1 {
				return true
			}
		}
		return false
	}, nil
}
