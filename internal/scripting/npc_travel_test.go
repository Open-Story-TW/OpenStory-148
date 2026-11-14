package scripting

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/scripts"
)

// travelHost answers level queries and records warps.
type travelHost struct {
	level int64
	warps [][]int64
}

func (h *travelHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch c.Kind {
	case "level":
		return CommandResult{Value: h.level}, nil
	case "warp":
		h.warps = append(h.warps, c.Ints)
		return CommandResult{}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}

func startNPC(t *testing.T, npcID int, host Host) (*Session, Event) {
	t.Helper()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("npc/%d.lua", npcID)
	source, err := scripts.Source(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Register(name, source); err != nil {
		t.Fatal(err)
	}
	s, event, err := e.Start(context.Background(), testIdentity, name, StartOptions{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	return s, event
}

func choiceIDs(p *Prompt) []int64 {
	ids := make([]int64, len(p.Choices))
	for i, c := range p.Choices {
		ids[i] = c.ID
	}
	return ids
}

func TestDimensionalMirrorOffersLevelBracketsAndWarps(t *testing.T) {
	for _, tc := range []struct {
		level int64
		want  string
	}{
		{10, "[801]"},
		{25, "[801 9 10 11]"},
		{45, "[801 2 11]"},
		{65, "[801 3 5 11]"},
		{120, "[801 11 12 13 14 15 16 17 21 22]"},
	} {
		host := &travelHost{level: tc.level}
		_, event := startNPC(t, 9010022, host)
		if event.Kind != "prompt" || event.Prompt.Kind != "map_selection" {
			t.Fatalf("level %d: %+v", tc.level, event)
		}
		if got := fmt.Sprint(choiceIDs(event.Prompt)); got != tc.want {
			t.Fatalf("level %d offered %s, want %s", tc.level, got, tc.want)
		}
	}

	host := &travelHost{level: 45}
	s, event := startNPC(t, 9010022, host)
	event = respond(t, s, event, Response{Choice: 2})
	if event.Kind != "done" || fmt.Sprint(host.warps) != "[[980000000 4]]" {
		t.Fatalf("warp %v after %+v", host.warps, event)
	}

	// An id the window did not offer is refused and nothing moves.
	host = &travelHost{level: 45}
	s, event = startNPC(t, 9010022, host)
	if _, err := s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "map_selection", Choice: 17}); err == nil || len(host.warps) != 0 {
		t.Fatalf("accepted an unoffered destination: %v %v", err, host.warps)
	}
}

func TestDimensionalMirrorRefusesUnderLevelTen(t *testing.T) {
	host := &travelHost{level: 9}
	s, event := startNPC(t, 9010022, host)
	if event.Prompt == nil || event.Prompt.Kind != "say" {
		t.Fatalf("%+v", event)
	}
	if event = respond(t, s, event, Response{Finish: true}); event.Kind != "done" || len(host.warps) != 0 {
		t.Fatalf("%+v %v", event, host.warps)
	}
}

func TestLeaWarpsToGuildHeadquartersOnYes(t *testing.T) {
	for _, yes := range []bool{true, false} {
		host := &travelHost{}
		s, event := startNPC(t, 2010011, host)
		if event.Prompt == nil || event.Prompt.Kind != "yes_no" {
			t.Fatalf("%+v", event)
		}
		event = respond(t, s, event, Response{Bool: yes})
		want := "[]"
		if yes {
			want = "[[200000301 0]]"
		}
		if event.Kind != "done" || fmt.Sprint(host.warps) != want {
			t.Fatalf("yes=%v: %v %+v", yes, host.warps, event)
		}
	}
}

func TestGodOfWealthGreets(t *testing.T) {
	host := &travelHost{}
	s, event := startNPC(t, 9310112, host)
	if event.Prompt == nil || event.Prompt.Kind != "say" {
		t.Fatalf("%+v", event)
	}
	if event = respond(t, s, event, Response{Finish: true}); event.Kind != "done" {
		t.Fatalf("%+v", event)
	}
}
