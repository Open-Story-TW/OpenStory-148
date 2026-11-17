package content

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
)

// The five professions are Skill.wz skills 9200-9204. A gathering profession
// comes first and unlocks the crafts that use what it gathers.
const (
	ProfessionHerbalism = 92000000
	ProfessionMining    = 92010000
	ProfessionSmithing  = 92020000
	ProfessionAccessory = 92030000
	ProfessionAlchemy   = 92040000
)

// ProfessionMaxLevel is the last level of Skill.wz 920x0000/level.
const ProfessionMaxLevel = 10

// ProfessionFatigueLimit is where the client stops offering to gather or
// craft. One point wears off every ProfessionFatigueSeconds.
const (
	ProfessionFatigueLimit   = 200
	ProfessionFatigueSeconds = 180
)

// ValidProfession reports whether id is one of the five profession skills.
func ValidProfession(id int) bool {
	switch id {
	case ProfessionHerbalism, ProfessionMining, ProfessionSmithing, ProfessionAccessory, ProfessionAlchemy:
		return true
	}
	return false
}

// ProfessionRequires is the gathering profession a craft needs first, or 0.
func ProfessionRequires(id int) int {
	switch id {
	case ProfessionSmithing, ProfessionAccessory:
		return ProfessionMining
	case ProfessionAlchemy:
		return ProfessionHerbalism
	}
	return 0
}

// ProfessionCraft reports whether id is one of the three crafts, of which a
// character holds at most one.
func ProfessionCraft(id int) bool { return ProfessionRequires(id) != 0 }

// RecipeItem is one stack a recipe uses or makes. Weight is the probWeight of
// a product among the recipe's products; ingredients leave it zero.
type RecipeItem struct {
	Item   int `json:"item"`
	Count  int `json:"count"`
	Weight int `json:"weight,omitempty"`
}

// Recipe is one Skill.wz Recipe_920x entry.
type Recipe struct {
	ID          int          `json:"id"`
	Profession  int          `json:"profession"`
	Products    []RecipeItem `json:"products"`
	Ingredients []RecipeItem `json:"ingredients"`
	Level       int          `json:"level"`       // reqSkillLevel
	Proficiency int          `json:"proficiency"` // incSkillProficiency; 0 means by level
	Fatigue     int          `json:"fatigue"`     // incFatigability
	Learned     bool         `json:"learned"`     // needOpenItem: a recipe item must be used first
	Period      int          `json:"period"`      // minutes the product lasts; 0 is forever
}

var professionRecipes sync.Map // root -> map[int]Recipe, per profession file

type recipeFileKey struct {
	root string
	file int
}

func recipeFile(root string, file int) (map[int]Recipe, error) {
	key := recipeFileKey{root, file}
	if v, ok := professionRecipes.Load(key); ok {
		return v.(map[int]Recipe), nil
	}
	node, err := readWZ(filepath.Join(root, "Skill.wz", fmt.Sprintf("Recipe_%d.img.xml", file)))
	if err != nil {
		return nil, err
	}
	out := make(map[int]Recipe, len(node.Children))
	for i := range node.Children {
		n := &node.Children[i]
		id, err := strconv.Atoi(n.Name)
		if err != nil || id/10000 != file {
			continue
		}
		r := Recipe{ID: id, Profession: file * 10000, Level: n.intValue("reqSkillLevel", 1), Proficiency: n.intValue("incSkillProficiency", 0), Fatigue: n.intValue("incFatigability", 0), Learned: n.intValue("needOpenItem", 0) != 0, Period: n.intValue("period", 0)}
		r.Products = recipeItems(n.child("target"))
		r.Ingredients = recipeItems(n.child("recipe"))
		if len(r.Products) == 0 || len(r.Ingredients) == 0 || r.Level < 1 || r.Level > ProfessionMaxLevel+2 || r.Fatigue < 0 || r.Fatigue > ProfessionFatigueLimit || r.Proficiency < 0 || r.Period < 0 {
			continue
		}
		out[id] = r
	}
	professionRecipes.Store(key, out)
	return out, nil
}

func recipeItems(n *wzNode) []RecipeItem {
	if n == nil {
		return nil
	}
	var out []RecipeItem
	for i := range n.Children {
		c := &n.Children[i]
		v := RecipeItem{Item: c.intValue("item", 0), Count: c.intValue("count", 0), Weight: c.intValue("probWeight", 0)}
		if v.Item < 1000000 || v.Item > 9999999 || v.Count < 1 || v.Count > 30000 || v.Weight < 0 {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// ProfessionRecipe reads one recipe. Without a WZ root there are no recipes.
func ProfessionRecipe(root string, id int) (Recipe, error) {
	if root == "" || !ValidProfession(id/10000*10000) {
		return Recipe{}, fmt.Errorf("unknown recipe %d", id)
	}
	recipes, err := recipeFile(root, id/10000)
	if err != nil {
		return Recipe{}, err
	}
	r, ok := recipes[id]
	if !ok {
		return Recipe{}, fmt.Errorf("unknown recipe %d", id)
	}
	return r, nil
}

var professionExpTables sync.Map // recipeFileKey -> [ProfessionMaxLevel+1]int

// ProfessionExpToNext is the proficiency a profession needs at level to reach
// the next one (Skill.wz 920x0000/level/N/x); 0 at the last level. The table
// is the same for the five professions; the fallback is the WZ one.
func ProfessionExpToNext(root string, profession, level int) int {
	if level < 1 || level >= ProfessionMaxLevel || !ValidProfession(profession) {
		return 0
	}
	fallback := [ProfessionMaxLevel]int{0, 250, 600, 1050, 1600, 2250, 3000, 3850, 4900, 5850}
	if root == "" {
		return fallback[level]
	}
	key := recipeFileKey{root, profession / 10000}
	v, ok := professionExpTables.Load(key)
	if !ok {
		table := fallback
		if node, err := readWZ(filepath.Join(root, "Skill.wz", fmt.Sprintf("%d.img.xml", profession/10000))); err == nil {
			levels := node.child("skill").child(strconv.Itoa(profession)).child("level")
			for i := 1; i < ProfessionMaxLevel; i++ {
				if x := levels.child(strconv.Itoa(i)).intValue("x", 0); x > 0 {
					table[i] = x
				}
			}
		}
		v, _ = professionExpTables.LoadOrStore(key, table)
	}
	return v.([ProfessionMaxLevel]int)[level]
}

// Reactor is one map object a player can gather from.
type Reactor struct {
	Index      int `json:"index"`
	TemplateID int `json:"template_id"`
	X          int `json:"x"`
	Y          int `json:"y"`
	Facing     int `json:"facing"`
	// Respawn is the map's reactorTime in seconds; 0 uses the template's.
	Respawn int `json:"respawn"`
}

// GatherNode describes a herb patch (template 1000xx) or an ore vein (2000xx).
type GatherNode struct {
	TemplateID int
	Profession int  // ProfessionHerbalism or ProfessionMining
	Level      int  // profession level the node is meant for
	Free       bool // info/notFatigue: gathering it costs no fatigue
	Respawn    int  // info/resetTime in seconds
}

var gatherNodes sync.Map // "root|id" -> GatherNode

// GatherNodeDefinition reads a gathering reactor template. Only the herb and
// vein templates are gathering nodes; any other reactor is refused.
func GatherNodeDefinition(root string, id int) (GatherNode, error) {
	if root == "" || id/100 != 1000 && id/100 != 2000 {
		return GatherNode{}, fmt.Errorf("reactor %d is not a gathering node", id)
	}
	key := root + "|" + strconv.Itoa(id)
	if v, ok := gatherNodes.Load(key); ok {
		return v.(GatherNode), nil
	}
	node, err := readWZ(filepath.Join(root, "Reactor.wz", fmt.Sprintf("%07d.img.xml", id)))
	if err != nil {
		return GatherNode{}, err
	}
	info := node.child("info")
	if info == nil || node.stringValue("action", "") == "" {
		return GatherNode{}, fmt.Errorf("reactor %d is not a gathering node", id)
	}
	g := GatherNode{TemplateID: id, Profession: ProfessionHerbalism, Level: info.intValue("level", 1), Free: info.intValue("notFatigue", 0) != 0, Respawn: info.intValue("resetTime", 5)}
	if id/100 == 2000 {
		g.Profession = ProfessionMining
	}
	if g.Level < 1 || g.Level > ProfessionMaxLevel+2 || g.Respawn < 0 || g.Respawn > 86400 {
		return GatherNode{}, fmt.Errorf("reactor %d has invalid gathering info", id)
	}
	gatherNodes.Store(key, g)
	return g, nil
}

var mapReactors sync.Map // "root|map" -> []Reactor

// HeliosElevatorDoor is the WZ reactor placed at each elevator entrance.
const HeliosElevatorDoor = 2208004

const (
	KrexelLobby   = 541020700
	KrexelArena   = 541020800
	KrexelReactor = 5411001
)

// MapReactors lists the supported reactors placed on a map.
func MapReactors(root string, mapID int) ([]Reactor, error) {
	if root == "" || mapID < 0 || mapID > 999999998 {
		return nil, nil
	}
	key := root + "|" + strconv.Itoa(mapID)
	if v, ok := mapReactors.Load(key); ok {
		return v.([]Reactor), nil
	}
	defs, err := readMapReactorDefinitions(root, mapID)
	if err != nil {
		return nil, err
	}
	var out []Reactor
	for _, def := range defs {
		id := def.TemplateID
		elevator := id == HeliosElevatorDoor && (mapID == 222020100 || mapID == 222020200)
		krexel := id == KrexelReactor && mapID == KrexelArena
		if !elevator && !krexel && !TreasureHuntReactor(mapID, id) && !EscapeReactor(mapID, id) && !RomeoLabReactor(mapID, id) {
			if _, err = GatherNodeDefinition(root, id); err != nil {
				continue
			}
		}
		out = append(out, def)
	}
	mapReactors.Store(key, out)
	return out, nil
}

func readMapReactorDefinitions(root string, mapID int) ([]Reactor, error) {
	node, err := readWZ(filepath.Join(root, "Map.wz", "Map", fmt.Sprintf("Map%d", mapID/100000000), fmt.Sprintf("%09d.img.xml", mapID)))
	if err != nil {
		return nil, err
	}
	var out []Reactor
	if list := node.child("reactor"); list != nil {
		for i := range list.Children {
			n := &list.Children[i]
			index, err := strconv.Atoi(n.Name)
			if err != nil {
				continue
			}
			id, err := strconv.Atoi(n.stringValue("id", ""))
			if err != nil {
				continue
			}
			out = append(out, Reactor{Index: index, TemplateID: id, X: n.intValue("x", 0), Y: n.intValue("y", 0), Facing: n.intValue("f", 0), Respawn: n.intValue("reactorTime", 0)})
		}
	}
	return out, nil
}

// Gathering by-products: what a failed attempt leaves, per profession.
const (
	HerbRoots    = 4022023 // 藥草的根
	OreFragments = 4011010 // 母礦的碎塊
)

// GatherTool is the equipment family a profession gathers with: shovels
// (150xxxx) for herbs and pickaxes (151xxxx) for ore.
func GatherTool(profession int) int {
	if profession == ProfessionMining {
		return 151
	}
	return 150
}

// StarterGatherTool is the tool a gathering master hands out, or 0.
func StarterGatherTool(profession int) int {
	switch profession {
	case ProfessionHerbalism:
		return 1502000
	case ProfessionMining:
		return 1512000
	}
	return 0
}

// GatherYield lists what a node can give. Skill.wz has no drop table for the
// nodes, so the yield is what the profession's own refining recipes consume:
// node template n (0-9) gives the raw materials of the recipes for level n+1,
// the practice node (10) gives the same as the first one, and the rare node
// (11) gives any of them. Catalysts (4024xxx) are bought, not gathered.
func GatherYield(root string, node GatherNode) ([]int, error) {
	recipes, err := recipeFile(root, node.Profession/10000)
	if err != nil {
		return nil, err
	}
	index := node.TemplateID % 100
	if index == 10 {
		index = 0
	}
	seen := map[int]bool{}
	var out []int
	for id := node.Profession + 1; id < node.Profession+100; id++ {
		r, ok := recipes[id]
		if !ok || index < 10 && r.Level != index+1 {
			continue
		}
		for _, in := range r.Ingredients {
			if in.Item/1000 == 4024 || seen[in.Item] {
				continue
			}
			seen[in.Item] = true
			out = append(out, in.Item)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("gathering node %d has no yield", node.TemplateID)
	}
	return out, nil
}

// RecipeScroll is a recipe item (251xxxx): using it learns Recipe once the
// recipe's profession is at Level.
type RecipeScroll struct {
	Item, Recipe, Level int
}

var recipeScrolls sync.Map // root -> map[int]RecipeScroll

// RecipeScrollDefinition reads Item.wz Consume/0251 spec/recipe.
func RecipeScrollDefinition(root string, item int) (RecipeScroll, error) {
	if root == "" || item/10000 != 251 {
		return RecipeScroll{}, fmt.Errorf("item %d is not a recipe", item)
	}
	v, ok := recipeScrolls.Load(root)
	if !ok {
		node, err := readWZ(filepath.Join(root, "Item.wz", "Consume", "0251.img.xml"))
		if err != nil {
			return RecipeScroll{}, err
		}
		all := make(map[int]RecipeScroll, len(node.Children))
		for i := range node.Children {
			n := &node.Children[i]
			id, err := strconv.Atoi(n.Name)
			spec := n.child("spec")
			if err != nil || spec == nil {
				continue
			}
			s := RecipeScroll{Item: id, Recipe: spec.intValue("recipe", 0), Level: spec.intValue("reqSkillLevel", 1)}
			if s.Recipe%10000 != 0 && ValidProfession(s.Recipe/10000*10000) {
				all[id] = s
			}
		}
		v, _ = recipeScrolls.LoadOrStore(root, all)
	}
	s, ok := v.(map[int]RecipeScroll)[item]
	if !ok {
		return RecipeScroll{}, fmt.Errorf("item %d is not a recipe", item)
	}
	return s, nil
}
