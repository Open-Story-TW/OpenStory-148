package content

import (
	_ "embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"maple-go-server/scripts"
)

type QuestItem struct {
	ID       int
	Count    int
	IfAbsent bool
	// Nil weight means unconditional (legacy prop absent or -2). Positive
	// weights share one random pool; -1 is a player-selected reward and zero
	// is not an unconditional reward.
	Weight     *int
	Gender     *int
	Job, JobEx int
}
type QuestRequirement struct {
	NPCID              int
	MinLevel, MaxLevel int
	MinFame            int
	Jobs               []int
	Quests             map[int]int
	Items              []QuestItem
	Mobs               map[int]int
	MobOrder           []int
	MapID              int
	MinHP              int
	Start, End         time.Time
	Script             string
	// Interval is the minutes after completion before a repeatable quest may
	// start again; zero means the quest is not repeatable.
	Interval int
	// InfoQuest and InfoEx are the WZ infoNumber/infoex demand: the custom
	// data record of quest InfoQuest (this quest when zero) must satisfy at
	// least one entry.
	InfoQuest int
	InfoEx    []QuestInfoCondition
	// SubJobFlags is the WZ subJobFlags mask: bit n admits characters whose
	// Explorer subcategory is n (1 ordinary, 2 Dual Blade, 4 Cannoneer,
	// 1024 Jett). Zero means no subcategory demand.
	SubJobFlags int
	// AutoStart is the WZ normalAutoStart flag: the client offers the quest
	// by itself wherever the character stands, so its start transition does
	// not need the NPC on the map.
	AutoStart   bool
	Unsupported []string
}

// QuestInfoCondition is one infoex entry. Cond 0 compares the record text
// with Value; cond 1 requires a number of at least Value. Variable selects one
// key of a "key=value;key=value" record.
type QuestInfoCondition struct {
	Value    string
	Cond     int
	Variable string
}
type QuestAction struct {
	EXP, Mesos  int64
	Fame        int
	Items       []QuestItem
	NextQuest   int
	HPDelta     int
	HPAtLeast   int
	ItemBuff    int
	Unsupported []string
}
type Quest struct {
	ID                  int
	Name                string
	Start, Complete     QuestRequirement
	OnStart, OnComplete QuestAction
	CanForfeit          bool
	Unsupported         []string
}
type questSources struct{ check, act, info *wzNode }

var questSourceCache sync.Map
var questCache sync.Map

//go:embed quests.json
var questSeed []byte

// questSupplement holds v148 Quest.wz entries the v145 data root lacks. The
// native client offers these quests itself; only reviewed lines are listed.
//
//go:embed quests_v148.json
var questSupplement []byte

type questSeedEntry struct {
	ID    int    `json:"id"`
	Check string `json:"check"`
	Act   string `json:"act"`
	Info  string `json:"info"`
}

func seedQuestSources(raw []byte, id int) (questSources, bool, error) {
	var seed struct {
		Quests []questSeedEntry `json:"quests"`
	}
	if err := json.Unmarshal(raw, &seed); err != nil {
		return questSources{}, false, err
	}
	for _, q := range seed.Quests {
		if q.ID != id {
			continue
		}
		sources := questSources{check: &wzNode{}, act: &wzNode{}, info: &wzNode{}}
		for _, part := range []struct {
			text   string
			target *wzNode
		}{{q.Check, sources.check}, {q.Act, sources.act}, {q.Info, sources.info}} {
			if part.text != "" {
				if err := xml.Unmarshal([]byte(part.text), part.target); err != nil {
					return questSources{}, false, err
				}
			}
		}
		return sources, true, nil
	}
	return questSources{}, false, nil
}

func (c *Catalog) Quest(id int) (Quest, error) { return QuestDefinition(c.root, id) }
func QuestDefinition(root string, id int) (Quest, error) {
	if id < 1 || id > 65535 {
		return Quest{}, fmt.Errorf("unsupported quest id")
	}
	key := fmt.Sprintf("%s/%d", root, id)
	if cached, ok := questCache.Load(key); ok {
		return cached.(Quest), nil
	}
	var sources questSources
	if root == "" {
		found := false
		var err error
		if sources, found, err = seedQuestSources(questSeed, id); err != nil {
			return Quest{}, err
		}
		if !found {
			if sources, found, err = seedQuestSources(questSupplement, id); err != nil {
				return Quest{}, err
			}
		}
		if !found {
			return Quest{}, fmt.Errorf("quest%d not in embedded catalog", id)
		}
	} else {
		cached, ok := questSourceCache.Load(root)
		if !ok {
			parts := questSources{}
			var err error
			parts.check, err = readWZ(filepath.Join(root, "Quest.wz", "Check.img.xml"))
			if err != nil {
				return Quest{}, err
			}
			parts.act, err = readWZ(filepath.Join(root, "Quest.wz", "Act.img.xml"))
			if err != nil {
				return Quest{}, err
			}
			parts.info, err = readWZ(filepath.Join(root, "Quest.wz", "QuestInfo.img.xml"))
			if err != nil {
				return Quest{}, err
			}
			cached, _ = questSourceCache.LoadOrStore(root, parts)
		}
		all := cached.(questSources)
		name := strconv.Itoa(id)
		sources = questSources{check: all.check.child(name), act: all.act.child(name), info: all.info.child(name)}
		if sources.check == nil {
			if supplement, found, err := seedQuestSources(questSupplement, id); err != nil {
				return Quest{}, err
			} else if found {
				sources = supplement
			}
		}
	}
	if sources.check == nil || sources.check.child("0") == nil || sources.check.child("1") == nil {
		return Quest{}, fmt.Errorf("quest%d has incomplete checks", id)
	}
	q := Quest{ID: id, Name: sources.info.stringValue("name", ""), CanForfeit: sources.info.intValue("resignBlocked", 0) == 0}
	q.Start = parseQuestRequirement(sources.check.child("0"))
	q.Complete = parseQuestRequirement(sources.check.child("1"))
	startAction := sources.act.child("0")
	q.OnStart = parseQuestAction(startAction, 0)
	q.OnComplete = parseQuestAction(sources.act.child("1"), id)
	if id == 7300 && q.Start.NPCID == 2081000 && startAction.intValue("ask", 0) == 1 {
		// The three stop rows only select the next dialogue response.
		stop := startAction.child("stop")
		if stop != nil && len(stop.Children) == 3 {
			valid := true
			for i, row := range stop.Children {
				valid = valid && row.Name == strconv.Itoa(i) && len(row.Children) == 1 && row.intValue("answer", 0) == 1
			}
			if valid {
				q.OnStart.Unsupported = slices.DeleteFunc(q.OnStart.Unsupported, func(field string) bool {
					return field == "ask" || field == "stop"
				})
			}
		}
	}
	if q.OnStart.ItemBuff != 0 || q.OnComplete.ItemBuff != 0 && id != 7301 && id != 7303 {
		q.Unsupported = append(q.Unsupported, "unreviewed quest item buff")
	}
	if sources.info.intValue("blocked", 0) != 0 {
		q.Unsupported = append(q.Unsupported, "blocked")
	}
	// Roger's Apple has scripted rewards/HP conditions absent from Act.wz.
	// Exact values are from scripts/quest/1021.js, not caller-provided overrides.
	if id == 1021 {
		q.Start.Script = "quest/1021.lua"
		q.Complete.Script = "quest/1021.lua"
		q.Complete.MinHP = 50
		q.OnStart.HPDelta = -30
		q.OnStart.HPAtLeast = 50
		q.OnStart.Items = []QuestItem{{ID: 2010007, Count: 1, IfAbsent: true}}
		q.OnComplete.EXP = 10
		q.OnComplete.Items = []QuestItem{{ID: 2010000, Count: 3}, {ID: 2010009, Count: 3}}
	} else if q.Start.Script != "" || q.Complete.Script != "" {
		// A WZ start/end script is only honoured through its reviewed Lua
		// conversion; the transition then requires that authenticated source.
		lua := fmt.Sprintf("quest/%d.lua", id)
		if _, err := scripts.Source(lua); err != nil {
			q.Unsupported = append(q.Unsupported, "unconverted quest script")
		} else {
			if q.Start.Script != "" {
				q.Start.Script = lua
			}
			if q.Complete.Script != "" {
				q.Complete.Script = lua
			}
		}
	}
	if id == 20000 || id == 20010 || id == 20015 || id == 20020 {
		q.CanForfeit = false
	}
	questCache.Store(key, q)
	return q, nil
}

// RemoteStart reports whether a transition may run away from the quest NPC:
// the start of a normalAutoStart quest without a start script, which the
// client sends by itself. Its start requirements still apply in full.
func (q Quest) RemoteStart(action string) bool {
	return action == "start" && q.Start.AutoStart && q.Start.Script == ""
}

func parseQuestRequirement(n *wzNode) QuestRequirement {
	r := QuestRequirement{MaxLevel: 999, Quests: map[int]int{}, Mobs: map[int]int{}, MapID: -1}
	if n == nil {
		r.Unsupported = append(r.Unsupported, "missing checks")
		return r
	}
	for _, x := range n.Children {
		switch x.Name {
		case "npc":
			r.NPCID = questInt(x, &r.Unsupported)
		case "lvmin":
			r.MinLevel = questInt(x, &r.Unsupported)
		case "lvmax":
			r.MaxLevel = questInt(x, &r.Unsupported)
		case "pop":
			r.MinFame = questInt(x, &r.Unsupported)
		case "fieldEnter":
			r.MapID = questInt(x, &r.Unsupported)
		case "job":
			for _, v := range x.Children {
				r.Jobs = append(r.Jobs, questInt(v, &r.Unsupported))
			}
		case "quest":
			for _, v := range x.Children {
				questFields(v, []string{"id", "state"}, &r.Unsupported)
				id := questChildInt(v, "id", 0, &r.Unsupported)
				// An entry without state demands "not started": the v148
				// Cygnus/Resistance path quests (20101-20105, 23011-23013)
				// list the sibling paths and themselves this way so only one
				// path can be taken.
				state := questChildInt(v, "state", 0, &r.Unsupported)
				if id < 1 || state < 0 || state > 2 {
					r.Unsupported = append(r.Unsupported, "invalid quest prerequisite")
				}
				r.Quests[id] = state
			}
		case "item":
			for _, v := range x.Children {
				for _, field := range v.Children {
					if field.Name != "id" && field.Name != "count" {
						r.Unsupported = append(r.Unsupported, "item."+field.Name)
					}
				}
				id, count := questChildInt(v, "id", 0, &r.Unsupported), questChildInt(v, "count", 0, &r.Unsupported)
				if id < 1 || count < 0 {
					r.Unsupported = append(r.Unsupported, "invalid item requirement")
				}
				r.Items = append(r.Items, QuestItem{ID: id, Count: count})
			}
		case "mob":
			for _, v := range x.Children {
				questFields(v, []string{"id", "count"}, &r.Unsupported)
				id, count := questChildInt(v, "id", 0, &r.Unsupported), questChildInt(v, "count", 0, &r.Unsupported)
				if id < 1 || count < 1 || count > 999 {
					r.Unsupported = append(r.Unsupported, "invalid mob requirement")
				}
				if _, exists := r.Mobs[id]; exists {
					r.Unsupported = append(r.Unsupported, "duplicate mob requirement")
				}
				r.Mobs[id] = count
				r.MobOrder = append(r.MobOrder, id)
			}
		case "start", "end":
			layout := "2006010215"
			if len(x.Value) == 12 {
				layout = "200601021504"
			}
			d, e := time.ParseInLocation(layout, x.Value, time.FixedZone("TMS", 8*3600))
			if e != nil {
				r.Unsupported = append(r.Unsupported, "date."+x.Name)
			} else if x.Name == "start" {
				r.Start = d
			} else {
				r.End = d
			}
		case "startscript", "endscript":
			r.Script = x.Value
		case "normalAutoStart": // remote offer; prerequisites still run.
			r.AutoStart = questInt(x, &r.Unsupported) != 0
		case "interval":
			r.Interval = questInt(x, &r.Unsupported)
			if r.Interval < 0 {
				r.Unsupported = append(r.Unsupported, x.Name)
			}
		case "infoNumber":
			r.InfoQuest = questInt(x, &r.Unsupported)
			if r.InfoQuest < 1 {
				r.Unsupported = append(r.Unsupported, x.Name)
			}
		case "infoex":
			for _, v := range x.Children {
				questFields(v, []string{"value", "cond", "exVariable"}, &r.Unsupported)
				entry := QuestInfoCondition{Cond: questChildInt(v, "cond", 0, &r.Unsupported)}
				if value := v.child("value"); value != nil {
					entry.Value = value.Value
				} else {
					r.Unsupported = append(r.Unsupported, "infoex without value")
				}
				if variable := v.child("exVariable"); variable != nil {
					entry.Variable = variable.Value
				}
				if entry.Cond != 0 && entry.Cond != 1 {
					r.Unsupported = append(r.Unsupported, fmt.Sprintf("infoex.cond %d", entry.Cond))
				}
				if _, err := strconv.Atoi(entry.Value); entry.Cond == 1 && err != nil {
					r.Unsupported = append(r.Unsupported, "infoex.cond 1 without a number")
				}
				r.InfoEx = append(r.InfoEx, entry)
			}
			if len(r.InfoEx) == 0 {
				r.Unsupported = append(r.Unsupported, x.Name)
			}
		case "start_t", "end_t": // Legacy never loads these alternate dates.
		case "subJobFlags":
			r.SubJobFlags = questInt(x, &r.Unsupported)
			if r.SubJobFlags <= 0 {
				r.Unsupported = append(r.Unsupported, x.Name)
			}
		default:
			r.Unsupported = append(r.Unsupported, x.Name)
		}
	}
	return r
}
func questInt(n wzNode, unsupported *[]string) int {
	v, e := strconv.Atoi(n.Value)
	if e != nil {
		*unsupported = append(*unsupported, "invalid number "+n.Name)
	}
	return v
}
func questChildInt(n wzNode, key string, fallback int, unsupported *[]string) int {
	child := n.child(key)
	if child == nil {
		return fallback
	}
	return questInt(*child, unsupported)
}
func questFields(n wzNode, allowed []string, unsupported *[]string) {
	seen := map[string]bool{}
	for _, field := range n.Children {
		ok := false
		for _, name := range allowed {
			if field.Name == name {
				ok = true
			}
		}
		if !ok || seen[field.Name] {
			*unsupported = append(*unsupported, "unsupported/duplicate field "+field.Name)
		}
		seen[field.Name] = true
	}
}
func parseQuestAction(n *wzNode, completingQuest int) QuestAction {
	a := QuestAction{}
	if n == nil {
		return a
	}
	for _, x := range n.Children {
		switch x.Name {
		case "exp":
			a.EXP = int64(questInt(x, &a.Unsupported))
		case "money":
			a.Mesos = int64(questInt(x, &a.Unsupported))
		case "pop":
			a.Fame = questInt(x, &a.Unsupported)
			if a.Fame < -30000 || a.Fame > 30000 {
				a.Unsupported = append(a.Unsupported, "invalid fame reward")
			}
		case "nextQuest":
			a.NextQuest = questInt(x, &a.Unsupported)
		case "buffItemID":
			id := questInt(x, &a.Unsupported)
			if id != HorntailDisguiseItem || a.ItemBuff != 0 {
				a.Unsupported = append(a.Unsupported, "unreviewed or duplicate quest buff item")
			} else {
				a.ItemBuff = id
			}
		case "item":
			for _, entry := range x.Children {
				questFields(entry, []string{"id", "count", "potentialGrade", "prop", "gender", "job", "jobEx", "var"}, &a.Unsupported)
				count := questChildInt(entry, "count", 0, &a.Unsupported)
				id := questChildInt(entry, "id", 0, &a.Unsupported)
				item := QuestItem{}
				for _, field := range entry.Children {
					switch field.Name {
					case "id", "count":
					case "potentialGrade":
						if field.Value != "一般" && field.Value != "" {
							a.Unsupported = append(a.Unsupported, "item.potentialGrade")
						}
					case "prop":
						value := questInt(field, &a.Unsupported)
						if value != -2 {
							if value < -1 || value > 1000000 {
								a.Unsupported = append(a.Unsupported, "item.prop")
							}
							item.Weight = &value
						}
					case "gender":
						value := questInt(field, &a.Unsupported)
						if value < -1 || value > 2 {
							a.Unsupported = append(a.Unsupported, "item.gender")
						}
						item.Gender = &value
					case "job":
						item.Job = questInt(field, &a.Unsupported)
					case "jobEx":
						item.JobEx = questInt(field, &a.Unsupported)
					case "var":
						value := questInt(field, &a.Unsupported)
						// The nearby Taiwanese Legacy server grants this quest cape with
						// its WZ base stats. Restrict that behavior to the original row.
						if completingQuest != 7103 || id != 1102256 || count != 1 || value != 2 {
							a.Unsupported = append(a.Unsupported, "item.var")
						}
					default:
						a.Unsupported = append(a.Unsupported, "item."+field.Name)
					}
				}
				if id < 1 || count < -30000 || count > 30000 {
					a.Unsupported = append(a.Unsupported, "invalid item action")
				}
				if count != 0 {
					item.ID, item.Count = id, count
					a.Items = append(a.Items, item)
				}
			}
		default:
			// Numbered strings and yes/no directories contain dialogue text in
			// Act.wz, not an executable reward operation.
			_, numeric := strconv.Atoi(x.Name)
			if (numeric == nil && x.XMLName.Local != "string") || (numeric != nil && x.Name != "yes" && x.Name != "no") {
				a.Unsupported = append(a.Unsupported, x.Name)
			} else if x.Name == "yes" || x.Name == "no" {
				for _, field := range x.Children {
					if _, err := strconv.Atoi(field.Name); err != nil || field.XMLName.Local != "string" {
						a.Unsupported = append(a.Unsupported, "dialogue."+field.Name)
					}
				}
			}
		}
	}
	return a
}
