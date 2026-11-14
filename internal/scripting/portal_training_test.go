package scripting

import (
	"context"
	"testing"

	"maple-go-server/scripts"
)

type trainingConversionHost struct {
	states   map[int64]int64
	commands []Command
}

func (h *trainingConversionHost) Execute(_ context.Context, id Identity, c Command) (CommandResult, error) {
	if id != testIdentity || c.Script != "portal/entertraining.lua" {
		return CommandResult{}, ErrIdentity
	}
	h.commands = append(h.commands, c)
	if c.Kind == "quest_state" {
		return CommandResult{Value: h.states[c.Ints[0]]}, nil
	}
	return CommandResult{}, nil
}
func TestTrainingPortalPreservesOrderedLegacyBranchesAndDenial(t *testing.T) {
	entries, err := scripts.ConditionalPortalMigrations()
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	entry := entries[0]
	if entry.SourceSHA256 != "7e9ef61deb880a99a23f9e294969d35c2c5754444b8ddb3940abd69d56ab91cc" || entry.SourceMapID != 1010000 || entry.SourcePortalName != "in00" {
		t.Fatal("legacy evidence changed", entry)
	}
	source, err := scripts.Source(entry.Lua)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := e.Register(entry.Lua, source)
	if err != nil || registered.SHA256 != entry.LuaSHA256 {
		t.Fatal("source hash", err)
	}
	for _, active := range []int64{0, 1041, 1042, 1043, 1044} {
		h := &trainingConversionHost{states: map[int64]int64{1041: 2, 1042: 0, 1043: 0, 1044: 0}}
		if active != 0 {
			h.states[active] = 1
		}
		s, event, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: h, API: "portal"})
		if err != nil || event.Kind != "done" {
			t.Fatalf("branch%d %+v %v", active, event, err)
		}
		s.Cancel()
		last := h.commands[len(h.commands)-1]
		if active == 0 {
			if last.Kind != "portal_message" || last.Text != entry.DeniedMessage {
				t.Fatal("denial changed", last)
			}
		} else {
			branch := entry.Branches[active-1041]
			if last.Kind != "warp" || len(last.Ints) != 2 || last.Ints[0] != int64(branch.MapID) || last.Ints[1] != 4 {
				t.Fatal("destination changed", last)
			}
		}
	}
	h := &trainingConversionHost{states: map[int64]int64{1041: 1, 1044: 1}}
	s, _, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: h, API: "portal"})
	if err != nil {
		t.Fatal(err)
	}
	s.Cancel()
	last := h.commands[len(h.commands)-1]
	if len(h.commands) != 2 || last.Ints[0] != 1010100 {
		t.Fatal("first-active branch order changed", h.commands)
	}
}
