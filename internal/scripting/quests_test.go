package scripting

import (
	"context"
	"fmt"
	"maple-go-server/scripts"
	"testing"
)

type questTestHost struct {
	state, hp int64
	commands  []Command
}

func (h *questTestHost) Execute(ctx context.Context, id Identity, c Command) (CommandResult, error) {
	if id != testIdentity {
		return CommandResult{}, ErrIdentity
	}
	h.commands = append(h.commands, c)
	switch c.Kind {
	case "quest_state":
		return CommandResult{Value: h.state}, nil
	case "hp":
		return CommandResult{Value: h.hp}, nil
	case "quest_start":
		if h.state != 0 {
			return CommandResult{}, fmt.Errorf("already started")
		}
		h.state = 1
		return CommandResult{Value: true}, nil
	case "quest_complete":
		if h.state != 1 {
			return CommandResult{}, fmt.Errorf("not started")
		}
		h.state = 2
		return CommandResult{Value: true}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}
func TestRogerConversionResumesFromPersistentQuestState(t *testing.T) {
	data, err := scripts.Source("quest/1021.lua")
	if err != nil {
		t.Fatal(err)
	}
	h := &questTestHost{hp: 50}
	e, err := New(Options{Host: h})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Register("quest/1021.lua", data); err != nil {
		t.Fatal(err)
	}
	s, event, err := e.Start(context.Background(), testIdentity, "quest/1021.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Back on page two preserves sequential locals and returns to page one.
	event, err = answer(s, event, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	event, err = s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: event.Prompt.Kind, Back: true})
	if err != nil {
		t.Fatal(err)
	}
	for h.state == 0 {
		event, err = answer(s, event, true, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	if event.Kind != "prompt" {
		t.Fatal("acceptance should commit before informational page")
	}
	s.Cancel()
	// New coroutine reads the durable quest state instead of replaying the grant.
	s, event, err = e.Start(context.Background(), testIdentity, "quest/1021.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	for event.Kind == "prompt" {
		event, err = answer(s, event, true, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	if h.state != 2 {
		t.Fatal("quest did not complete")
	}
	starts, completions := 0, 0
	seen := map[string]bool{}
	for _, c := range h.commands {
		if c.Script != "quest/1021.lua" || seen[c.ID] {
			t.Fatalf("untrusted/repeated identity: %+v", c)
		}
		seen[c.ID] = true
		if c.Kind == "quest_start" {
			starts++
		}
		if c.Kind == "quest_complete" {
			completions++
		}
	}
	if starts != 1 || completions != 1 {
		t.Fatalf("duplicate rewards %d %d", starts, completions)
	}
}
func TestQuestCommandValidationBeforeHost(t *testing.T) {
	h := &questTestHost{}
	e, _ := engineFor(t, `npc:quest_start(65536)`, Options{Host: h})
	s, _, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if s != nil {
		s.Cancel()
	}
	if err == nil || len(h.commands) != 0 {
		t.Fatal("invalid quest reached host")
	}
}

type pathQuestHost struct {
	state    int64
	record   string
	commands []string
}

func (h *pathQuestHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	if c.Script != "quest/1400.lua" {
		return CommandResult{}, fmt.Errorf("untrusted source %q", c.Script)
	}
	h.commands = append(h.commands, c.Kind)
	switch c.Kind {
	case "quest_state":
		return CommandResult{Value: h.state}, nil
	case "quest_start":
		if h.state != 0 {
			return CommandResult{}, fmt.Errorf("already started")
		}
		h.state = 1
		return CommandResult{Value: true}, nil
	case "exchange":
		if h.state != 1 || len(c.Exchange.QuestData) != 1 || c.Exchange.QuestData[0].QuestID != 1406 || len(c.Exchange.Items) != 0 || c.Exchange.MesosDelta != 0 {
			return CommandResult{}, fmt.Errorf("unexpected exchange %+v", c.Exchange)
		}
		h.record = c.Exchange.QuestData[0].Data
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	case "quest_complete":
		if h.state != 1 || h.record == "" {
			return CommandResult{}, fmt.Errorf("completed before the path was recorded")
		}
		h.state = 2
		return CommandResult{Value: true}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}

func TestFivePathsConversionRecordsChoiceAndResumes(t *testing.T) {
	data, err := scripts.Source("quest/1400.lua")
	if err != nil {
		t.Fatal(err)
	}
	h := &pathQuestHost{}
	e, err := New(Options{Host: h})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Register("quest/1400.lua", data); err != nil {
		t.Fatal(err)
	}
	run := func(choice int64, stopAfterStart bool) {
		s, event, err := e.Start(context.Background(), testIdentity, "quest/1400.lua", StartOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel()
		for event.Kind == "prompt" {
			if stopAfterStart && h.state == 1 {
				return
			}
			if event.Prompt.Kind == "menu" && len(event.Prompt.Choices) != 5 {
				t.Fatalf("choices=%d", len(event.Prompt.Choices))
			}
			if event.Prompt.Kind == "say" && !event.Prompt.AllowNext {
				event, err = s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "say", Finish: true})
			} else {
				event, err = answer(s, event, true, choice)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// Disconnect after acceptance: the next conversation asks again and
	// completes without starting the quest twice.
	run(0, true)
	if h.state != 1 || h.record != "" {
		t.Fatalf("state=%d record=%q", h.state, h.record)
	}
	run(3, false)
	if h.state != 2 || h.record != "4" {
		t.Fatalf("state=%d record=%q", h.state, h.record)
	}
	starts := 0
	for _, kind := range h.commands {
		if kind == "quest_start" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("starts=%d", starts)
	}
	before := len(h.commands)
	run(1, false)
	if h.state != 2 || h.record != "4" || len(h.commands) != before+1 {
		t.Fatal("completed quest was replayed")
	}
}
