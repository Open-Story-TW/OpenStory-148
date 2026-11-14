package scripting

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/scripts"
)

type advancementHost struct {
	job, level int64
	advances   int
}

func (h *advancementHost) Execute(_ context.Context, _ Identity, command Command) (CommandResult, error) {
	switch command.Kind {
	case "job":
		return CommandResult{Value: h.job}, nil
	case "level":
		return CommandResult{Value: h.level}, nil
	case "advance_warrior":
		if command.Script != "npc/1022000.lua" || command.ID == "" || len(command.Ints) != 0 {
			return CommandResult{}, fmt.Errorf("untrusted advancement command")
		}
		h.advances++
		h.job = 100
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", command.Kind)
}

func TestWarriorLuaOnlyAdvancesAfterConfirmationAndResumesFromJob(t *testing.T) {
	source, err := scripts.Source("npc/1022000.lua")
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Register("npc/1022000.lua", source); err != nil {
		t.Fatal(err)
	}
	for _, accept := range []bool{false, true} {
		host := &advancementHost{level: 10}
		session, event, err := e.Start(context.Background(), testIdentity, "npc/1022000.lua", StartOptions{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		for event.Kind == "prompt" && event.Prompt.Kind != "yes_no" {
			event, err = answer(session, event, true, 0)
			if err != nil {
				t.Fatal(err)
			}
		}
		if host.advances != 0 {
			t.Fatal("advanced before consent")
		}
		event, err = answer(session, event, accept, 0)
		if err != nil || event.Kind != "prompt" || (host.advances == 1) != accept {
			t.Fatalf("confirmation: %+v %v %+v", event, err, host)
		}
		session.Cancel() // Simulate disconnect before the final informational page.
		if accept {
			next, event, err := e.Start(context.Background(), testIdentity, "npc/1022000.lua", StartOptions{Host: host})
			if err != nil || event.Kind != "prompt" || host.advances != 1 {
				t.Fatalf("reconnect repeated advancement: %+v %v", event, err)
			}
			next.Cancel()
		}
	}
}

func TestWarriorLuaRejectsBelowRequiredLevel(t *testing.T) {
	source, _ := scripts.Source("npc/1022000.lua")
	e, _ := New(Options{})
	_, _ = e.Register("npc/1022000.lua", source)
	host := &advancementHost{level: 9}
	session, event, err := e.Start(context.Background(), testIdentity, "npc/1022000.lua", StartOptions{Host: host})
	if err != nil || event.Kind != "prompt" || event.Prompt.Kind != "say" || event.Prompt.AllowNext || host.advances != 0 {
		t.Fatalf("level gate: %+v %v", event, err)
	}
	session.Cancel()
}
