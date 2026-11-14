package content

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
)

type Skill struct {
	ID          int  `json:"id"`
	MaxLevel    int  `json:"max_level"`
	MasterLevel int  `json:"master_level"`
	ReqLevel    int  `json:"req_level"`
	Disabled    bool `json:"disabled"`
	Invisible   bool `json:"invisible"`
	Passive     bool `json:"passive"`
	// CombatOrders skills (WZ combatOrders=1) reach MaxLevel+2 under the
	// COMBAT_ORDERS buff (Java Skill.trueMax).
	CombatOrders bool                      `json:"combat_orders,omitempty"`
	Requirements map[int]int               `json:"requirements"`
	Common       map[string]string         `json:"common"`
	Levels       map[int]map[string]string `json:"levels"`
	// Range is the common lt/rb rectangle; LevelRanges the per-level one.
	Range       *SkillRange        `json:"range,omitempty"`
	LevelRanges map[int]SkillRange `json:"level_ranges,omitempty"`
	Info        map[string]int     `json:"info,omitempty"`
	// SummonAttack is summon/attack1/info (mobCount, attackCount): summon
	// skills such as Gaviota 5211002 keep their target count there, not in
	// the level data.
	SummonAttack map[string]int `json:"summon_attack,omitempty"`
}

// SkillRange is a WZ lt/rb pair relative to the caster facing left.
type SkillRange struct {
	LT [2]int `json:"lt"`
	RB [2]int `json:"rb"`
}

// TrueMax is the highest usable level: MaxLevel, plus 2 for CombatOrders.
func (s Skill) TrueMax() int {
	if s.CombatOrders {
		return s.MaxLevel + 2
	}
	return s.MaxLevel
}

// RangeAt returns the lt/rb range of a level.
func (s Skill) RangeAt(level int) (SkillRange, bool) {
	if r, ok := s.LevelRanges[level]; ok {
		return r, true
	}
	if r, ok := s.LevelRanges[s.MaxLevel]; ok && level > s.MaxLevel {
		return r, true
	}
	if s.Range != nil {
		return *s.Range, true
	}
	return SkillRange{}, false
}

func (s Skill) Values(level int) (map[string]int, error) {
	if level < 1 || level > s.TrueMax() {
		return nil, fmt.Errorf("invalid skill level")
	}
	source := s.Common
	if source == nil {
		// A per-level table has no rows above MaxLevel; Java getEffect
		// then uses the last level.
		source = s.Levels[min(level, s.MaxLevel)]
	}
	if source == nil {
		return nil, fmt.Errorf("missing skill level")
	}
	out := map[string]int{}
	for key, value := range source {
		if skillLabel(value) {
			continue // A string attribute such as action="slashStorm2".
		}
		v, e := SkillFormula(value, level)
		if e != nil {
			return nil, fmt.Errorf("skill%d.%s: %w", s.ID, key, e)
		}
		out[key] = v
	}
	return out, nil
}

// skillLabel reports a non-formula WZ string (an identifier longer than a
// formula variable, with no operators or calls).
func skillLabel(value string) bool {
	if len(value) < 2 {
		return false
	}
	for i, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && (r >= '0' && r <= '9' || r == '_')) {
			return false
		}
	}
	return true
}

//go:embed skills.json
var skillSeed []byte
var skillSeedOnce sync.Once
var skillSeeds map[int]Skill
var skillSeedError error
var skillTemplates sync.Map

func SkillDefinition(root string, id int) (Skill, error) {
	if id < 1 || id > 99999999 {
		return Skill{}, fmt.Errorf("invalid skill id")
	}
	key := fmt.Sprintf("%s/%d", root, id)
	if v, ok := skillTemplates.Load(key); ok {
		return v.(Skill), nil
	}
	var s Skill
	if root == "" {
		skillSeedOnce.Do(func() {
			var list []Skill
			skillSeedError = json.Unmarshal(skillSeed, &list)
			skillSeeds = map[int]Skill{}
			for _, v := range list {
				skillSeeds[v.ID] = v
			}
		})
		if skillSeedError != nil {
			return s, skillSeedError
		}
		var ok bool
		s, ok = skillSeeds[id]
		if !ok {
			return s, fmt.Errorf("unknown skill%d", id)
		}
	} else {
		file := filepath.Join(root, "Skill.wz", fmt.Sprintf("%03d.img.xml", id/10000))
		n, e := readWZ(file)
		if e != nil {
			return s, e
		}
		n = n.child("skill")
		if n == nil {
			return s, fmt.Errorf("missing skill node")
		}
		var found *wzNode
		for i := range n.Children {
			v := &n.Children[i]
			parsed, _ := strconv.Atoi(v.Name)
			if parsed == id {
				found = v
				break
			}
		}
		if found == nil {
			return s, fmt.Errorf("unknown skill%d", id)
		}
		s = skillFromNode(id, found)
	}
	if s.MaxLevel < 1 || s.MaxLevel > 255 {
		return Skill{}, fmt.Errorf("invalid skill maxlevel")
	}
	skillTemplates.Store(key, s)
	return s, nil
}
func skillFromNode(id int, n *wzNode) Skill {
	s := Skill{ID: id, MaxLevel: 0, MasterLevel: n.intValue("masterLevel", 0), ReqLevel: n.intValue("reqLev", 0), Disabled: n.intValue("disable", 0) != 0, Invisible: n.intValue("invisible", 0) != 0, Passive: n.intValue("skillType", 0) == 2, CombatOrders: n.intValue("combatOrders", 0) > 0, Requirements: map[int]int{}}
	values := func(n *wzNode) map[string]string {
		if n == nil {
			return nil
		}
		v := map[string]string{}
		for _, x := range n.Children {
			if x.XMLName.Local == "int" || x.XMLName.Local == "string" {
				if x.Name != "hs" {
					v[x.Name] = x.Value
				}
			}
		}
		return v
	}
	vectors := func(n *wzNode) (SkillRange, bool) {
		var r SkillRange
		found := 0
		for _, x := range n.Children {
			if x.XMLName.Local != "vector" || x.Name != "lt" && x.Name != "rb" {
				continue
			}
			px, _ := strconv.Atoi(x.X)
			py, _ := strconv.Atoi(x.Y)
			if x.Name == "lt" {
				r.LT = [2]int{px, py}
			} else {
				r.RB = [2]int{px, py}
			}
			found++
		}
		return r, found > 0
	}
	if c := n.child("common"); c != nil {
		s.Common = values(c)
		s.MaxLevel = c.intValue("maxLevel", 0)
		if r, ok := vectors(c); ok {
			s.Range = &r
		}
	} else if l := n.child("level"); l != nil {
		s.Levels = map[int]map[string]string{}
		for i := range l.Children {
			lv, _ := strconv.Atoi(l.Children[i].Name)
			s.Levels[lv] = values(&l.Children[i])
			s.MaxLevel = max(s.MaxLevel, lv)
			if r, ok := vectors(&l.Children[i]); ok {
				if s.LevelRanges == nil {
					s.LevelRanges = map[int]SkillRange{}
				}
				s.LevelRanges[lv] = r
			}
		}
	}
	if info := n.child("info"); info != nil {
		for _, x := range info.Children {
			if x.XMLName.Local == "int" {
				if v, e := strconv.Atoi(x.Value); e == nil {
					if s.Info == nil {
						s.Info = map[string]int{}
					}
					s.Info[x.Name] = v
				}
			}
		}
	}
	if a := n.child("summon").child("attack1").child("info"); a != nil {
		for _, x := range a.Children {
			if x.XMLName.Local == "int" {
				if v, e := strconv.Atoi(x.Value); e == nil {
					if s.SummonAttack == nil {
						s.SummonAttack = map[string]int{}
					}
					s.SummonAttack[x.Name] = v
				}
			}
		}
	}
	if r := n.child("req"); r != nil {
		for _, v := range r.Children {
			k, _ := strconv.Atoi(v.Name)
			num, _ := strconv.Atoi(v.Value)
			s.Requirements[k] = num
		}
	}
	return s
}

// Deprecated: CanLearnAdventurer predates the other job families; it now
// applies the all-family ancestry of CanLearn.
func (s Skill) CanLearnAdventurer(job int) bool { return s.CanLearn(job) }
