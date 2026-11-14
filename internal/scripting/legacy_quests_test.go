package scripting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"maple-go-server/scripts"
)

// legacyQuestHost models the durable state a generated quest source can read
// or change, so every converted script can be driven without a database.
type legacyQuestHost struct {
	script    string
	states    map[int64]int64
	data      map[int64]string
	items     map[int64]int64
	exp       int64
	exchanges int
	warped    bool
	full      bool
}

func (h *legacyQuestHost) Execute(_ context.Context, id Identity, c Command) (CommandResult, error) {
	if id != testIdentity || c.Script != h.script {
		return CommandResult{}, ErrIdentity
	}
	switch c.Kind {
	case "quest_state":
		return CommandResult{Value: h.states[c.Ints[0]]}, nil
	case "quest_data":
		return CommandResult{Value: h.data[c.Ints[0]]}, nil
	case "job":
		return CommandResult{Value: int64(100)}, nil
	case "level":
		return CommandResult{Value: int64(200)}, nil
	case "item_count":
		return CommandResult{Value: h.items[c.Ints[0]]}, nil
	case "can_hold":
		return CommandResult{Value: !h.full}, nil
	case "warp":
		h.warped = true
		return CommandResult{}, nil
	case "exchange":
		h.exchanges++
		x := c.Exchange
		if len(x.Logs) != 0 || x.Travel != nil {
			return CommandResult{}, fmt.Errorf("unexpected exchange %+v", x)
		}
		for _, item := range x.Items {
			if h.items[int64(item.TemplateID)]+int64(item.Quantity) < 0 || h.full && item.Quantity > 0 {
				return CommandResult{Value: map[string]any{"ok": false, "reason": "conditions_not_met"}}, nil
			}
		}
		for _, item := range x.Items {
			h.items[int64(item.TemplateID)] += int64(item.Quantity)
		}
		h.exp += x.EXP
		for _, quest := range x.Quests {
			h.states[int64(quest.QuestID)] = int64(quest.State)
			if quest.Data != nil {
				h.data[int64(quest.QuestID)] = *quest.Data
			}
		}
		for _, change := range x.QuestData {
			h.data[int64(change.QuestID)] = change.Data
		}
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}

func runLegacyQuest(t *testing.T, e *Engine, name string, h *legacyQuestHost, pick func(p *Prompt) Response) {
	t.Helper()
	s, event, err := e.Start(context.Background(), testIdentity, name, StartOptions{Host: h})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	defer s.Cancel()
	for steps := 0; event.Kind == "prompt"; steps++ {
		if steps > 400 {
			t.Fatalf("%s: dialogue did not end", name)
		}
		p := event.Prompt
		if p.Kind != "menu" && (p.Text == "" || strings.Contains(p.Text, "#L")) {
			t.Fatalf("%s: bad prompt %+v", name, p)
		}
		r := pick(p)
		r.Identity, r.PromptID, r.Kind = testIdentity, p.ID, p.Kind
		if event, err = s.Respond(context.Background(), r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if event.Kind != "done" {
		t.Fatalf("%s: ended with %s", name, event.Kind)
	}
}

// TestGeneratedLegacyQuestsRunToCompletion drives every generated quest source
// through its start and end phases with accepting, declining and random
// answers. It proves the conversion compiles and that each reachable branch
// uses only host commands the driver provides.
func TestGeneratedLegacyQuestsRunToCompletion(t *testing.T) {
	migrations, err := scripts.LegacyQuestMigrations()
	if err != nil || len(migrations) < 100 {
		t.Fatalf("migrations=%d err=%v", len(migrations), err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	accept := func(p *Prompt) Response {
		r := Response{Bool: true}
		if p.Kind == "menu" {
			r.Choice = p.Choices[0].ID
		}
		if p.Kind == "say" && !p.AllowNext {
			r.Finish = true
		}
		return r
	}
	decline := func(p *Prompt) Response {
		r := accept(p)
		if p.Kind == "yes_no" || p.Kind == "accept" {
			r.Bool = false
		}
		return r
	}
	for _, m := range migrations {
		raw, err := scripts.RawSource(m.Lua)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != m.LuaSHA256 || !scripts.LegacyQuest(m.Lua) {
			t.Fatalf("%s does not match its manifest entry", m.Lua)
		}
		source, err := scripts.Source(m.Lua)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Register(m.Lua, source); err != nil {
			t.Fatalf("%s: %v", m.Lua, err)
		}
		var quest int64
		fmt.Sscanf(m.Lua, "quest/%d.lua", &quest)
		random := rand.New(rand.NewSource(quest))
		chaotic := func(p *Prompt) Response {
			r := accept(p)
			switch p.Kind {
			case "menu":
				r.Choice = p.Choices[random.Intn(len(p.Choices))].ID
			case "yes_no", "accept":
				r.Bool = random.Intn(3) > 0
			case "say":
				if p.AllowBack && random.Intn(4) == 0 {
					r.Bool, r.Finish, r.Back = false, false, true
				}
			}
			return r
		}
		t.Run(m.Lua, func(t *testing.T) {
			for _, state := range []int64{0, 1} {
				for round, pick := range []func(*Prompt) Response{accept, decline, chaotic, chaotic, chaotic, accept, accept} {
					h := &legacyQuestHost{script: m.Lua, states: map[int64]int64{quest: state}, data: map[int64]string{}, items: map[int64]int64{}}
					switch round {
					case 5: // the player owns everything a script may ask for
						h.items = everyItem{}.fill(source)
					case 6: // rewards cannot be stored
						h.full = true
					}
					runLegacyQuest(t, e, m.Lua, h, pick)
				}
			}
		})
	}
}

type everyItem struct{}

func (everyItem) fill(source []byte) map[int64]int64 {
	out := map[int64]int64{}
	text := string(source)
	for _, call := range []string{"qm.haveItem(", "qm.gainItem("} {
		for rest := text; ; {
			i := strings.Index(rest, call)
			if i < 0 {
				break
			}
			rest = rest[i+len(call):]
			var id int64
			if _, err := fmt.Sscanf(rest, "%d", &id); err == nil {
				out[id] = 30000
			}
		}
	}
	return out
}

// A forced completion and its reward must reach the host as one exchange, and
// a refused exchange must not show the reward text.
func TestLegacyQuestDriverCommitsOneInvocationAtomically(t *testing.T) {
	source := scripts.LegacyQuestMarker + `
__quest = 2186
status = -1
function start(mode, type, selection)
    qm.dispose()
end
function finish(mode, type, selection)
    status = status + 1
    if status == 0 then
        qm.sendNext("thanks")
    else
        qm.gainItem(2030019, 5)
        qm.gainExp(1700)
        qm.forceCompleteQuest()
        qm.sendOk("#L0#first#l#L1#second#l")
        qm.dispose()
    end
end
` + "\n" + string(scripts.LegacyQuestDriver())
	for _, full := range []bool{false, true} {
		e, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Register("quest/2186.lua", []byte(source)); err != nil {
			t.Fatal(err)
		}
		h := &legacyQuestHost{script: "quest/2186.lua", states: map[int64]int64{2186: 1}, data: map[int64]string{}, items: map[int64]int64{}, full: full}
		var last *Prompt
		runLegacyQuest(t, e, "quest/2186.lua", h, func(p *Prompt) Response {
			last = p
			r := Response{Bool: true}
			if p.Kind == "menu" {
				r.Choice = 1
			}
			if p.Kind == "say" && !p.AllowNext {
				r.Finish = true
			}
			return r
		})
		if h.exchanges != 1 {
			t.Fatalf("full=%v exchanges=%d", full, h.exchanges)
		}
		if full {
			if h.states[2186] != 1 || h.exp != 0 || last.Kind != "say" || strings.Contains(last.Text, "first") {
				t.Fatalf("refused exchange leaked: %+v %+v", h, last)
			}
			continue
		}
		if h.states[2186] != 2 || h.exp != 1700 || h.items[2030019] != 5 {
			t.Fatalf("reward not applied: %+v", h)
		}
		if last.Kind != "menu" || len(last.Choices) != 2 || last.Choices[1].Text != "second" || last.Text != "" {
			t.Fatalf("menu not parsed: %+v", last)
		}
	}
}
