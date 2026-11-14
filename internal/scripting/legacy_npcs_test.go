package scripting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/scripts"
)

// legacyNPCHost models what a generated Legacy NPC source can read or change,
// so every converted conversation can run without a world server.
type legacyNPCHost struct {
	script    string
	rich      bool
	states    map[int64]int64
	data      map[int64]string
	ended     string // the warp, shop or storage that closed the conversation
	exchanges int
	opened    int64
}

func (h *legacyNPCHost) Execute(_ context.Context, id Identity, c Command) (CommandResult, error) {
	if id != testIdentity || c.Script != h.script {
		return CommandResult{}, ErrIdentity
	}
	if h.ended != "" {
		return CommandResult{}, fmt.Errorf("%s after %s", c.Kind, h.ended)
	}
	pick := func(poor, rich int64) (CommandResult, error) {
		if h.rich {
			return CommandResult{Value: rich}, nil
		}
		return CommandResult{Value: poor}, nil
	}
	switch c.Kind {
	case "quest_state":
		return CommandResult{Value: h.states[c.Ints[0]]}, nil
	case "quest_data":
		return CommandResult{Value: h.data[c.Ints[0]]}, nil
	case "map_id":
		return CommandResult{Value: int64(100000000)}, nil
	case "job":
		return pick(0, 112)
	case "level":
		return pick(1, 200)
	case "gender":
		return pick(0, 1)
	case "hp":
		return CommandResult{Value: int64(50)}, nil
	case "mesos":
		return pick(0, 2000000000)
	case "item_count":
		return pick(0, 30000)
	case "can_hold":
		return CommandResult{Value: h.rich}, nil
	case "name":
		return CommandResult{Value: "測試角色"}, nil
	case "message":
		if c.Text == "" {
			return CommandResult{}, fmt.Errorf("empty message")
		}
		return CommandResult{}, nil
	case "hair":
		return pick(30000, 31057)
	case "face":
		return pick(20000, 21302)
	case "stat":
		return pick(0, 30)
	case "has_equipped":
		return CommandResult{Value: h.rich}, nil
	case "player_count":
		// Odd rooms are full for the rich character.
		return pick(0, 3*(c.Ints[0]%2))
	case "monster_count":
		return pick(0, 3)
	case "party":
		if h.rich {
			return CommandResult{Value: map[string]any{"id": int64(7), "leader": true, "size": int64(3)}}, nil
		}
		return CommandResult{Value: map[string]any{"id": int64(0), "leader": false, "size": int64(0)}}, nil
	case "map_message":
		return CommandResult{}, nil
	case "style_filter":
		kept := []int64{}
		for _, id := range c.Ints {
			if id >= 0 && id <= 13 || id >= 20000 && id <= 49999 {
				kept = append(kept, id)
			}
		}
		return CommandResult{Value: kept}, nil
	case "change_look":
		return CommandResult{Value: map[string]any{"ok": h.rich}}, nil
	case "quest_start", "quest_complete":
		if c.Ints[0] >= 100000 {
			return CommandResult{}, fmt.Errorf("quest %d has no game data", c.Ints[0])
		}
		h.states[c.Ints[0]] = map[string]int64{"quest_start": 1, "quest_complete": 2}[c.Kind]
		return CommandResult{Value: true}, nil
	case "open_npc":
		h.opened = c.Ints[0]
		return CommandResult{}, nil
	case "warp", "open_shop", "open_storage":
		h.ended = c.Kind
		return CommandResult{}, nil
	case "exchange":
		h.exchanges++
		for _, change := range c.Exchange.QuestData {
			h.data[int64(change.QuestID)] = change.Data
		}
		for _, change := range c.Exchange.Quests {
			h.states[int64(change.QuestID)] = int64(change.State)
		}
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}

// TestGeneratedLegacyNPCsRun talks to every generated NPC source as a new
// character with nothing and as a rich high-level one, answering its prompts
// with a seeded mix of choices. It proves each conversion compiles, uses only
// commands the NPC API binds, never issues a command after its warp, and
// never fails inside the converted Legacy statements.
func TestGeneratedLegacyNPCsRun(t *testing.T) {
	migrations, pending, err := scripts.LegacyNPCMigrations()
	if err != nil || len(migrations) < 300 || len(pending) == 0 {
		t.Fatalf("migrations=%d pending=%d err=%v", len(migrations), len(pending), err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	conversations, prompts := 0, 0
	for _, m := range migrations {
		raw, err := scripts.RawSource(m.Lua)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != m.LuaSHA256 || !scripts.LegacyNPC(m.Lua) {
			t.Fatalf("%s does not match its manifest entry", m.Lua)
		}
		source, err := scripts.Source(m.Lua)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Register(m.Lua, source); err != nil {
			t.Errorf("%s: %v", m.Lua, err)
			continue
		}
		for round := 0; round < 24; round++ {
			random := rand.New(rand.NewSource(int64(round)))
			h := &legacyNPCHost{script: m.Lua, rich: round%2 == 1, states: map[int64]int64{}, data: map[int64]string{}}
			if round%4 >= 2 {
				// Every quest the source asks about is in progress (or done).
				for _, line := range strings.Split(string(raw), "\n") {
					for _, call := range []string{"getQuestStatus(", "isQuestActive(", "isQuestFinished(", "isQuestStarted(", "getQuestRecord("} {
						if i := strings.Index(line, call); i >= 0 {
							var id int64
							if _, err := fmt.Sscanf(line[i+len(call):], "%d", &id); err == nil {
								h.states[id] = int64(1 + round/4%2)
								h.data[id] = "1"
							}
						}
					}
				}
			}
			s, event, err := e.Start(context.Background(), testIdentity, m.Lua, StartOptions{Host: h})
			for steps := 0; err == nil && event.Kind == "prompt" && steps < 60; steps++ {
				p := event.Prompt
				prompts++
				response := Response{Identity: testIdentity, PromptID: p.ID, Kind: p.Kind}
				switch p.Kind {
				case "say":
					switch {
					case !p.AllowNext && !p.AllowBack:
						response.Finish = true
					case !p.AllowNext:
						response.Back = true
					case p.AllowBack && round >= 8 && random.Intn(4) == 0:
						response.Back = true
					}
				case "yes_no", "accept":
					response.Bool = round < 8 || random.Intn(3) > 0
				case "menu":
					response.Choice = p.Choices[random.Intn(len(p.Choices))].ID
					if round < 2 {
						response.Choice = p.Choices[0].ID
					}
				case "avatar":
					response.Choice = int64(random.Intn(len(p.Choices)))
				case "text":
					response.Text = []string{"1", "5", "abc", "100"}[random.Intn(4)]
				default:
					t.Fatalf("%s: unexpected prompt %s", m.Lua, p.Kind)
				}
				event, err = s.Respond(context.Background(), response)
			}
			if s != nil {
				s.Cancel()
			}
			if err != nil {
				t.Errorf("%s (round %d): %v", m.Lua, round, err)
				break
			}
			conversations++
		}
	}
	t.Logf("%d generated NPC sources, %d conversations, %d prompts answered, %d Legacy scripts pending", len(migrations), conversations, prompts, len(pending))
}

// TestLegacyNPCDriverCommitsBeforeItWarps checks the driver's ordering: the
// effects one legacy call collected commit as one exchange before the warp,
// and the prompt the same call asked for is dropped with the conversation.
func TestLegacyNPCDriverCommitsBeforeItWarps(t *testing.T) {
	source := scripts.LegacyNPCMarker + `
__npc = 1
function __init() fee = __array(2, {100, 1000}) end
function start()
    cm.sendSimple("Where to?#b\r\n#L0#Near#l\r\n#L1#Far")
end
function action(mode, type_, selection)
    if cm.getMeso() < fee[selection] then
        cm.sendOk("no")
    else
        cm.gainMeso(-fee[selection])
        cm.gainItem(4000000, 1)
        cm.warp(104000000, "sp")
        cm.sendOk("dropped")
    end
    cm.dispose()
end
` + "\n" + string(scripts.LegacyNPCDriver())
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Register("npc/ordering.lua", []byte(source)); err != nil {
		t.Fatal(err)
	}
	h := &npcOrderHost{script: "npc/ordering.lua"}
	s, event, err := e.Start(context.Background(), testIdentity, "npc/ordering.lua", StartOptions{Host: h})
	if err != nil || event.Prompt == nil || event.Prompt.Kind != "menu" || len(event.Prompt.Choices) != 2 || event.Prompt.Choices[1].Text != "Far" || event.Prompt.Text != "Where to?#b\r\n" {
		t.Fatalf("event=%+v prompt=%+v err=%v", event, event.Prompt, err)
	}
	event, err = s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "menu", Choice: 1})
	if err != nil || event.Kind != "done" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	if got := strings.Join(h.kinds, ","); got != "mesos,exchange,warp" {
		t.Fatalf("commands = %s", got)
	}
	x := h.exchange
	if x == nil || x.MesosDelta != -1000 || len(x.Items) != 1 || x.Items[0].TemplateID != 4000000 || h.warp.Ints[0] != 104000000 || h.warp.Text != "sp" {
		t.Fatalf("exchange=%+v warp=%+v", x, h.warp)
	}
}

// npcOrderHost answers a rich character and keeps what the script did.
type npcOrderHost struct {
	script   string
	kinds    []string
	exchange *domain.ScriptExchange
	warp     Command
}

func (h *npcOrderHost) Execute(_ context.Context, id Identity, c Command) (CommandResult, error) {
	if id != testIdentity || c.Script != h.script {
		return CommandResult{}, ErrIdentity
	}
	h.kinds = append(h.kinds, c.Kind)
	switch c.Kind {
	case "mesos":
		return CommandResult{Value: int64(5000)}, nil
	case "exchange":
		h.exchange = c.Exchange
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	case "warp":
		h.warp = c
		return CommandResult{}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}
