package scripting

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/scripts"
)

type departureTestHost struct {
	level, mesos, letters int64
	departures            int
	accept                bool
}

func (h *departureTestHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch c.Kind {
	case "level":
		return CommandResult{Value: h.level}, nil
	case "mesos":
		return CommandResult{Value: h.mesos}, nil
	case "item_count":
		if len(c.Ints) != 1 || c.Ints[0] != 4031801 {
			return CommandResult{}, fmt.Errorf("wrong letter query")
		}
		return CommandResult{Value: h.letters}, nil
	case "depart_island":
		h.departures++
		return CommandResult{Value: map[string]any{"ok": h.accept}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected %s", c.Kind)
}
func TestShanksSequentialBranchesKeepCancelSeparateFromNo(t *testing.T) {
	for _, c := range []struct {
		name                  string
		level, mesos, letters int64
		rejectPrompt          int
		cancel                bool
		want                  int
	}{
		{"paid", 5, 150, 0, -1, false, 1}, {"recommendation-low-level", 1, 0, 1, -1, false, 1}, {"low-level", 4, 150, 0, -1, false, 0}, {"insufficient", 5, 149, 0, -1, false, 0}, {"no", 5, 150, 0, 0, false, 0}, {"previous", 1, 0, 1, 1, false, 0}, {"end", 5, 150, 0, 0, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := &departureTestHost{level: c.level, mesos: c.mesos, letters: c.letters, accept: true}
			e, _ := New(Options{Host: h})
			source, err := scripts.Source("npc/22000.lua")
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Register("npc/22000.lua", source)
			if err != nil {
				t.Fatal(err)
			}
			s, event, err := e.Start(context.Background(), testIdentity, "npc/22000.lua", StartOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel()
			for step := 0; event.Kind == "prompt" && step < 8; step++ {
				if c.cancel && step == c.rejectPrompt {
					s.Cancel()
					break
				}
				yes := step != c.rejectPrompt
				event, err = s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: event.Prompt.Kind, Bool: yes, Back: !yes && event.Prompt.Kind == "say", Finish: event.Prompt.Kind == "say" && !event.Prompt.AllowNext})
				if err != nil {
					t.Fatal(err)
				}
			}
			if h.departures != c.want {
				t.Fatalf("departures=%d want=%d event=%+v", h.departures, c.want, event)
			}
		})
	}
}
