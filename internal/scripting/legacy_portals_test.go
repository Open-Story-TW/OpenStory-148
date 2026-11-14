package scripting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"maple-go-server/scripts"
)

// legacyPortalHost models what a generated Legacy portal source can read or
// change, so every converted script can run without a world server.
type legacyPortalHost struct {
	script    string
	level     int64
	states    map[int64]int64
	data      map[int64]string
	items     map[int64]int64
	players   int64
	commands  []Command
	warps     []Command
	exchanges []Command
	opened    int64
}

func (h *legacyPortalHost) Execute(_ context.Context, id Identity, c Command) (CommandResult, error) {
	if id != testIdentity || c.Script != h.script {
		return CommandResult{}, ErrIdentity
	}
	if len(h.warps) > 0 {
		return CommandResult{}, fmt.Errorf("%s after the warp", c.Kind)
	}
	h.commands = append(h.commands, c)
	switch c.Kind {
	case "quest_state":
		return CommandResult{Value: h.states[c.Ints[0]]}, nil
	case "quest_data":
		return CommandResult{Value: h.data[c.Ints[0]]}, nil
	case "map_id":
		return CommandResult{Value: int64(100000000)}, nil
	case "job":
		return CommandResult{Value: int64(100)}, nil
	case "level":
		return CommandResult{Value: h.level}, nil
	case "gender", "hp":
		return CommandResult{Value: int64(1)}, nil
	case "mesos":
		return CommandResult{Value: int64(1000)}, nil
	case "item_count":
		return CommandResult{Value: h.items[c.Ints[0]]}, nil
	case "can_hold":
		return CommandResult{Value: true}, nil
	case "portal_name":
		return CommandResult{Value: "in00"}, nil
	case "portal_id":
		return CommandResult{Value: int64(3)}, nil
	case "player_count", "monster_count":
		return CommandResult{Value: h.players}, nil
	case "portal_message":
		if c.Text == "" {
			return CommandResult{}, fmt.Errorf("empty message")
		}
		return CommandResult{}, nil
	case "open_npc":
		h.opened = c.Ints[0]
		return CommandResult{}, nil
	case "warp":
		h.warps = append(h.warps, c)
		return CommandResult{}, nil
	case "exchange":
		h.exchanges = append(h.exchanges, c)
		for _, change := range c.Exchange.QuestData {
			h.data[int64(change.QuestID)] = change.Data
		}
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}

// TestGeneratedLegacyPortalsRun runs every generated portal source against
// a low-level empty character and a high-level one holding every queried
// item and quest. It proves each conversion compiles, uses only commands the
// portal API binds, and never issues a command after its warp.
func TestGeneratedLegacyPortalsRun(t *testing.T) {
	migrations, pending, err := scripts.LegacyPortalMigrations()
	if err != nil || len(migrations) < 300 || len(pending) == 0 {
		t.Fatalf("migrations=%d pending=%d err=%v", len(migrations), len(pending), err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	warped := 0
	for _, m := range migrations {
		raw, err := scripts.RawSource(m.Lua)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != m.LuaSHA256 || !scripts.LegacyPortal(m.Lua) {
			t.Fatalf("%s does not match its manifest entry", m.Lua)
		}
		source, err := scripts.Source(m.Lua)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Register(m.Lua, source); err != nil {
			t.Fatalf("%s: %v", m.Lua, err)
		}
		for _, rich := range []bool{false, true} {
			h := &legacyPortalHost{script: m.Lua, level: 1, states: map[int64]int64{}, data: map[int64]string{}, items: map[int64]int64{}}
			if rich {
				h.level, h.players = 200, 0
				for _, line := range strings.Split(string(raw), "\n") {
					for _, call := range []string{"haveItem(", "getQuestStatus(", "isQuestActive(", "isQuestFinished(", "itemQuantity("} {
						if i := strings.Index(line, call); i >= 0 {
							var id int64
							if _, err := fmt.Sscanf(line[i+len(call):], "%d", &id); err == nil {
								h.items[id] = 100
								h.states[id] = 1
							}
						}
					}
				}
			} else {
				h.players = 5
			}
			s, event, err := e.Start(context.Background(), testIdentity, m.Lua, StartOptions{Host: h, API: "portal"})
			if err != nil {
				t.Fatalf("%s (rich=%v): %v", m.Lua, rich, err)
			}
			s.Cancel()
			if event.Kind != "done" {
				t.Fatalf("%s (rich=%v): ended with %s", m.Lua, rich, event.Kind)
			}
			if len(h.warps) > 1 {
				t.Fatalf("%s: %d warps", m.Lua, len(h.warps))
			}
			if len(h.warps) == 1 {
				warped++
			}
		}
	}
	if warped < len(migrations) {
		t.Fatalf("only %d of %d runs warped", warped, 2*len(migrations))
	}
	t.Logf("%d generated portal sources, %d of %d runs warped, %d Legacy scripts pending", len(migrations), warped, 2*len(migrations), len(pending))
}

// TestLegacyPortalDriverKeepsLastWarpAndCommitsFirst checks the driver's
// ordering: collected effects commit before the single, last warp.
func TestLegacyPortalDriverKeepsLastWarpAndCommitsFirst(t *testing.T) {
	source := scripts.LegacyPortalMarker + `
function enter(pi)
    pi.saveLocation("FREE_MARKET")
    pi.forceCompleteQuest(2000)
    pi.gainItem(4000000, -1)
    pi.warp(100000000, 0)
    pi.playerMessage(5, "after the first warp")
    pi.warp(910000000, "st00")
    pi.openNpc(9010000)
end`
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := scripts.Source("portal/go_FantasticPark.lua")
	if err != nil {
		t.Fatal(err)
	}
	full := source + "\n" + string(driver[strings.Index(string(driver), "\n-- Shared driver")+1:])
	if _, err = e.Register("portal/ordering.lua", []byte(full)); err != nil {
		t.Fatal(err)
	}
	h := &legacyPortalHost{script: "portal/ordering.lua", level: 30, states: map[int64]int64{}, data: map[int64]string{}, items: map[int64]int64{}}
	if _, event, err := e.Start(context.Background(), testIdentity, "portal/ordering.lua", StartOptions{Host: h, API: "portal"}); err != nil || event.Kind != "done" {
		t.Fatalf("%+v %v", event, err)
	}
	kinds := []string{}
	for _, c := range h.commands {
		kinds = append(kinds, c.Kind)
	}
	if got := strings.Join(kinds, ","); got != "map_id,portal_message,exchange,open_npc,warp" {
		t.Fatalf("commands %s", got)
	}
	x := h.exchanges[0].Exchange
	if len(x.QuestData) != 1 || x.QuestData[0].QuestID != 1900000000 || x.QuestData[0].Data != "100000000" ||
		len(x.Quests) != 1 || x.Quests[0].QuestID != 2000 || x.Quests[0].State != 2 || len(x.Items) != 1 || x.Items[0].Quantity != -1 {
		t.Fatalf("exchange %+v", x)
	}
	if w := h.warps[0]; w.Ints[0] != 910000000 || w.Text != "st00" || h.opened != 9010000 {
		t.Fatalf("warp %+v npc %d", w, h.opened)
	}
}
