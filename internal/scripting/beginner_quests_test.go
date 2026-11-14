package scripting

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/scripts"
)

// beginnerQuestHost is legacyQuestHost plus the job advancement command the
// reviewed beginner job sources call.
type beginnerQuestHost struct {
	legacyQuestHost
	advanced []int64
	refuse   bool
}

func (h *beginnerQuestHost) Execute(ctx context.Context, id Identity, c Command) (CommandResult, error) {
	if c.Kind == "advance_first_job" {
		if id != testIdentity || c.Script != h.script || len(c.Ints) != 1 {
			return CommandResult{}, ErrIdentity
		}
		if h.refuse {
			return CommandResult{Value: map[string]any{"ok": false}}, nil
		}
		h.advanced = append(h.advanced, c.Ints[0])
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	if c.Kind == "quest_start" {
		if id != testIdentity || c.Script != h.script || len(c.Ints) != 1 {
			return CommandResult{}, ErrIdentity
		}
		h.states[c.Ints[0]] = 1
		return CommandResult{Value: true}, nil
	}
	return h.legacyQuestHost.Execute(ctx, id, c)
}

func TestReviewedBeginnerQuestSources(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		quest  int64
		states map[int64]int64
		items  map[int64]int64
		job    int64
		// done is the quest state the source leaves when it does not
		// advance a job (the server completes job quests itself).
		done int64
	}{
		{quest: 2570, states: map[int64]int64{2570: 1}, job: 501},
		{quest: 20101, states: map[int64]int64{20101: 1}, job: 1100},
		{quest: 20102, states: map[int64]int64{20102: 1}, job: 1200},
		{quest: 20103, states: map[int64]int64{20103: 1}, job: 1300},
		{quest: 20104, states: map[int64]int64{20104: 1}, job: 1400},
		{quest: 20105, states: map[int64]int64{20105: 1}, job: 1500},
		{quest: 21101, states: map[int64]int64{21100: 2}, job: 2100},
		{quest: 23011, states: map[int64]int64{23011: 1}, job: 3200},
		{quest: 23012, states: map[int64]int64{23012: 1}, job: 3300},
		{quest: 23013, states: map[int64]int64{23013: 1}, job: 3500},
		{quest: 20001, states: map[int64]int64{20000: 2}, done: 2},
		{quest: 20002, states: map[int64]int64{20001: 2}, done: 2},
		{quest: 20013, states: map[int64]int64{20012: 2}, done: 1},
		{quest: 20013, states: map[int64]int64{20013: 1}, items: map[int64]int64{4032267: 1, 4032268: 1}, done: 2},
		{quest: 20016, states: map[int64]int64{20015: 2}, done: 2},
		{quest: 20017, states: map[int64]int64{20016: 2}, done: 1},
		// Dual Blade, v148 line at Lady Syl.
		{quest: 2623, states: map[int64]int64{2622: 2}, done: 1},
		{quest: 2623, states: map[int64]int64{2623: 1}, items: map[int64]int64{4032616: 1}, job: 430},
		{quest: 2638, states: map[int64]int64{2637: 2}, done: 1},
		{quest: 2638, states: map[int64]int64{2638: 1}, items: map[int64]int64{4031013: 30}, job: 431},
		{quest: 2642, states: map[int64]int64{2642: 1}, job: 432},
	} {
		name := fmt.Sprintf("quest/%d.lua", tc.quest)
		source, err := scripts.Source(name)
		if err != nil {
			t.Fatal(err)
		}
		if scripts.LegacyQuest(name) {
			t.Fatalf("%s is a reviewed source, not a generated one", name)
		}
		if _, err = e.Register(name, source); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		items := tc.items
		if items == nil {
			items = map[int64]int64{}
		}
		h := &beginnerQuestHost{legacyQuestHost: legacyQuestHost{script: name, states: tc.states, data: map[int64]string{}, items: items}}
		runReviewedQuest(t, e, name, h)
		if tc.job != 0 {
			if len(h.advanced) != 1 || h.advanced[0] != tc.job || h.exchanges != 0 {
				t.Fatalf("%s advanced %v with %d exchanges", name, h.advanced, h.exchanges)
			}
			// A refused advancement (full bag, wrong job) ends the talk.
			h = &beginnerQuestHost{legacyQuestHost: legacyQuestHost{script: name, states: tc.states, data: map[int64]string{}, items: map[int64]int64{}}, refuse: true}
			runReviewedQuest(t, e, name, h)
			continue
		}
		if h.states[tc.quest] != tc.done || len(h.advanced) != 0 {
			t.Fatalf("%s left state %d, advanced %v", name, h.states[tc.quest], h.advanced)
		}
		// Finished quests are not replayed.
		h.states[tc.quest] = 2
		before := h.exchanges
		runReviewedQuest(t, e, name, h)
		if h.exchanges != before {
			t.Fatalf("%s replayed a finished quest", name)
		}
	}
}

func runReviewedQuest(t *testing.T, e *Engine, name string, h *beginnerQuestHost) {
	t.Helper()
	s, event, err := e.Start(context.Background(), testIdentity, name, StartOptions{Host: h})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	defer s.Cancel()
	for steps := 0; event.Kind == "prompt"; steps++ {
		if steps > 50 {
			t.Fatalf("%s: dialogue did not end", name)
		}
		p := event.Prompt
		r := Response{Identity: testIdentity, PromptID: p.ID, Kind: p.Kind, Bool: true}
		if p.Kind == "say" && !p.AllowNext {
			r.Finish = true
		}
		if event, err = s.Respond(context.Background(), r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if event.Kind != "done" {
		t.Fatalf("%s: ended with %s", name, event.Kind)
	}
}
