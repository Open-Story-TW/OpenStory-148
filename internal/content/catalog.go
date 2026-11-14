// Package content loads versioned, read-only gameplay definitions from WZ XML.
package content

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type Portal struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Type       int    `json:"type"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	TargetMap  int    `json:"target_map"`
	TargetName string `json:"target_name"`
	Script     string `json:"script,omitempty"`
}

type Life struct {
	Index          int    `json:"index"`
	Kind           string `json:"kind"`
	TemplateID     int    `json:"template_id"`
	X              int    `json:"x"`
	Y              int    `json:"y"`
	Foothold       int    `json:"foothold"`
	Facing         int    `json:"facing"`
	RX0            int    `json:"rx0"`
	RX1            int    `json:"rx1"`
	Hidden         bool   `json:"hidden"`
	RespawnSeconds int    `json:"respawn_seconds"`
	Name           string `json:"name,omitempty"`
}

type Foothold struct {
	ID int `json:"id"`
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}

type Map struct {
	ID          int        `json:"id"`
	ReturnMap   int        `json:"return_map"`
	OnUserEnter string     `json:"on_user_enter,omitempty"`
	FieldLimit  int64      `json:"field_limit"`
	Town        bool       `json:"town"`
	Fly         bool       `json:"fly,omitempty"` // WZ info/fly: Soaring (x026/x142) allowed
	Portals     []Portal   `json:"portals"`
	Life        []Life     `json:"life"`
	Footholds   []Foothold `json:"footholds"`
}

func (m Map) Portal(name string) (Portal, bool) {
	for _, p := range m.Portals {
		if p.Name == name {
			return p, true
		}
	}
	return Portal{}, false
}

func (m Map) Spawn() (Portal, bool) {
	for _, p := range m.Portals {
		if p.Type == 0 {
			return p, true
		}
	}
	return Portal{}, false
}

type Mob struct {
	ID               int   `json:"id"`
	Level            int   `json:"level"`
	HP               int64 `json:"hp"`
	MP               int   `json:"mp"`
	EXP              int64 `json:"exp"`
	PAD              int   `json:"pad"`
	PDD              int   `json:"pdd"`
	Boss             bool  `json:"boss"`
	PDRate           int   `json:"pd_rate"`
	OnlyNormalAttack bool  `json:"only_normal_attack,omitempty"`
	Invincible       bool  `json:"invincible,omitempty"`
	FixedDamage      int   `json:"fixed_damage,omitempty"`
	HasFixedDamage   bool  `json:"has_fixed_damage,omitempty"`
	// S0b combat fields; zero means "normal" when absent from the seed.
	MDD      int    `json:"mdd,omitempty"`
	MDRate   int    `json:"md_rate,omitempty"`
	EVA      int    `json:"eva,omitempty"`
	ElemAttr string `json:"elem_attr,omitempty"` // WZ "F2I3": element letter + 1 immune/2 strong/3 weak.
	Undead   bool   `json:"undead,omitempty"`
}

//go:embed starter.json
var starter []byte

type Catalog struct {
	root      string
	mu        sync.RWMutex
	maps      map[int]Map
	mobs      map[int]Mob
	mobBodies map[int]MobBodyBounds
}

func NewCatalog(root string) (*Catalog, error) {
	c := &Catalog{root: root, maps: make(map[int]Map), mobs: make(map[int]Mob), mobBodies: make(map[int]MobBodyBounds)}
	var bundle struct {
		Version int   `json:"version"`
		Maps    []Map `json:"maps"`
		Mobs    []Mob `json:"mobs"`
	}
	if err := json.Unmarshal(starter, &bundle); err != nil {
		return nil, err
	}
	if bundle.Version != 1 {
		return nil, fmt.Errorf("unsupported starter content version")
	}
	for _, m := range bundle.Maps {
		c.maps[m.ID] = m
	}
	for _, m := range bundle.Mobs {
		c.mobs[m.ID] = m
	}
	if root == "" {
		bodies, err := loadEmbeddedMobBodies(mobBodySeed, c.mobs)
		if err != nil {
			return nil, err
		}
		c.mobBodies = bodies
	}
	return c, nil
}

func DefaultCatalog() (*Catalog, error) { return NewCatalog(os.Getenv("MAPLEGO_WZ_ROOT")) }

func (c *Catalog) Map(id int) (Map, error) {
	if id < 0 || id > 999999998 {
		return Map{}, fmt.Errorf("invalid map id %d", id)
	}
	c.mu.RLock()
	m, ok := c.maps[id]
	c.mu.RUnlock()
	if ok {
		return m, nil
	}
	if c.root == "" {
		return Map{}, fmt.Errorf("map %d is absent from embedded content", id)
	}
	node, err := readWZ(filepath.Join(c.root, "Map.wz", "Map", fmt.Sprintf("Map%d", id/100000000), fmt.Sprintf("%09d.img.xml", id)))
	if err != nil {
		return Map{}, err
	}
	m, err = parseMap(id, node)
	if err != nil {
		return Map{}, err
	}
	c.mu.Lock()
	c.maps[id] = m
	c.mu.Unlock()
	return m, nil
}

func (c *Catalog) Mob(id int) (Mob, error) {
	if id < 1 || id > 9999999 {
		return Mob{}, fmt.Errorf("invalid mob id %d", id)
	}
	c.mu.RLock()
	m, ok := c.mobs[id]
	c.mu.RUnlock()
	if ok {
		return m, nil
	}
	if c.root == "" {
		return Mob{}, fmt.Errorf("mob %d is absent from embedded content", id)
	}
	node, err := readWZ(filepath.Join(c.root, "Mob.wz", fmt.Sprintf("%07d.img.xml", id)))
	if err != nil {
		return Mob{}, err
	}
	info := node.child("info")
	if info == nil {
		return Mob{}, fmt.Errorf("mob %d has no info", id)
	}
	// Mob info/link shares animation assets, not combat attributes. Java's
	// MapleLifeFactory reads maxHP/EXP/etc from this monster's own info before
	// consulting the linked file's movement/attack animation properties.
	m = parseMob(id, info)
	if m.HP < 1 {
		return Mob{}, fmt.Errorf("mob %d has no positive maxHP", id)
	}
	c.mu.Lock()
	c.mobs[id] = m
	c.mu.Unlock()
	return m, nil
}

type wzNode struct {
	XMLName  xml.Name
	Name     string   `xml:"name,attr"`
	Value    string   `xml:"value,attr"`
	X        string   `xml:"x,attr"`
	Y        string   `xml:"y,attr"`
	Children []wzNode `xml:",any"`
}

func (n *wzNode) child(name string) *wzNode {
	if n == nil {
		return nil
	}
	for i := range n.Children {
		if n.Children[i].Name == name {
			return &n.Children[i]
		}
	}
	return nil
}
func (n *wzNode) stringValue(name, fallback string) string {
	if p := n.child(name); p != nil {
		return p.Value
	}
	return fallback
}
func (n *wzNode) intValue(name string, fallback int) int {
	p := n.child(name)
	if p == nil {
		return fallback
	}
	v, err := strconv.Atoi(p.Value)
	if err != nil {
		return fallback
	}
	return v
}
func readWZ(path string) (*wzNode, error) {
	f, err := openWZ(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 32<<20 {
		return nil, fmt.Errorf("WZ XML exceeds 32 MiB")
	}
	var n wzNode
	if err := xml.NewDecoder(bytes.NewReader(b)).Decode(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

func parseMob(id int, info *wzNode) Mob {
	return Mob{ID: id, Level: info.intValue("level", 1), HP: int64(info.intValue("maxHP", 0)), MP: info.intValue("maxMP", 0), EXP: int64(info.intValue("exp", 0)), PAD: info.intValue("PADamage", 0), PDD: info.intValue("PDDamage", 0), Boss: info.intValue("boss", 0) != 0, PDRate: info.intValue("PDRate", 0), OnlyNormalAttack: info.intValue("onlyNormalAttack", 0) != 0, Invincible: info.intValue("invincible", 0) != 0, FixedDamage: info.intValue("fixedDamage", 0), HasFixedDamage: info.child("fixedDamage") != nil, MDD: info.intValue("MDDamage", 0), MDRate: info.intValue("MDRate", 0), EVA: info.intValue("eva", 0), ElemAttr: info.stringValue("elemAttr", ""), Undead: info.intValue("undead", 0) != 0}
}

func parseMap(id int, n *wzNode) (Map, error) {
	i := n.child("info")
	if i == nil {
		return Map{}, fmt.Errorf("map %d has no info", id)
	}
	m := Map{ID: id, ReturnMap: i.intValue("returnMap", id), OnUserEnter: i.stringValue("onUserEnter", ""), FieldLimit: int64(i.intValue("fieldLimit", 0)), Town: i.intValue("town", 0) != 0, Fly: i.intValue("fly", 0) != 0}
	if p := n.child("portal"); p != nil {
		for _, x := range p.Children {
			pid, err := strconv.Atoi(x.Name)
			if err != nil {
				return Map{}, fmt.Errorf("invalid portal index")
			}
			m.Portals = append(m.Portals, Portal{ID: pid, Name: x.stringValue("pn", ""), Type: x.intValue("pt", 0), X: x.intValue("x", 0), Y: x.intValue("y", 0), TargetMap: x.intValue("tm", 999999999), TargetName: x.stringValue("tn", ""), Script: x.stringValue("script", "")})
		}
	}
	if l := n.child("life"); l != nil {
		for _, x := range l.Children {
			idx, err := strconv.Atoi(x.Name)
			if err != nil {
				continue
			}
			m.Life = append(m.Life, Life{Index: idx, Kind: x.stringValue("type", ""), TemplateID: x.intValue("id", 0), X: x.intValue("x", 0), Y: x.intValue("cy", x.intValue("y", 0)), Foothold: x.intValue("fh", 0), Facing: x.intValue("f", 0), RX0: x.intValue("rx0", 0), RX1: x.intValue("rx1", 0), Hidden: x.intValue("hide", 0) != 0, RespawnSeconds: x.intValue("mobTime", 0)})
		}
	}
	if f := n.child("foothold"); f != nil {
		for _, layer := range f.Children {
			for _, group := range layer.Children {
				for _, x := range group.Children {
					fid, err := strconv.Atoi(x.Name)
					if err != nil {
						continue
					}
					m.Footholds = append(m.Footholds, Foothold{ID: fid, X1: x.intValue("x1", 0), Y1: x.intValue("y1", 0), X2: x.intValue("x2", 0), Y2: x.intValue("y2", 0)})
				}
			}
		}
	}
	if _, ok := m.Spawn(); !ok {
		return Map{}, fmt.Errorf("map %d has no spawn portal", id)
	}
	return m, nil
}
