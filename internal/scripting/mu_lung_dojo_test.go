package scripting

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type dojoHost struct {
	mapID   int64
	reason  string
	inside  bool
	started *int
	left    *int
}

func (h dojoHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch {
	case c.Kind == "map_id":
		return CommandResult{Value: h.mapID}, nil
	case c.Kind == "event_start" && c.Text == "MuLungDojo":
		*h.started++
		return CommandResult{Value: map[string]any{"ok": h.reason == "", "reason": h.reason, "here": int64(1), "qualified": int64(1)}}, nil
	case c.Kind == "event_status" && c.Text == "MuLungDojo":
		return CommandResult{Value: map[string]any{"known": false, "busy": false, "members": int64(0), "inside": h.inside}}, nil
	case c.Kind == "event_leave":
		*h.left++
		return CommandResult{}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected dojo command %+v", c)
}

func TestSoGongStartsAndLeavesTheDojo(t *testing.T) {
	started, left := 0, 0
	session, event := startNPC(t, 2091005, dojoHost{mapID: 925020001, started: &started, left: &left})
	if event.Prompt == nil || len(event.Prompt.Choices) != 2 {
		t.Fatalf("hall menu %+v", event)
	}
	event = respond(t, session, event, Response{Choice: 0})
	if event = respond(t, session, event, Response{Bool: true}); event.Kind != "done" || started != 1 {
		t.Fatalf("start %+v started=%d", event, started)
	}

	session, event = startNPC(t, 2091005, dojoHost{mapID: 925020001, reason: "busy", started: &started, left: &left})
	event = respond(t, session, event, Response{Choice: 0})
	event = respond(t, session, event, Response{Bool: true})
	if event.Prompt == nil || !strings.Contains(event.Prompt.Text, "所有的道場") || started != 2 {
		t.Fatalf("busy answer %+v", event)
	}

	session, event = startNPC(t, 2091005, dojoHost{mapID: 925020300, inside: true, started: &started, left: &left})
	if event = respond(t, session, event, Response{Bool: true}); event.Kind != "done" || left != 1 {
		t.Fatalf("leave %+v left=%d", event, left)
	}
	if _, event = startNPC(t, 2091005, dojoHost{mapID: 925020300, started: &started, left: &left}); event.Kind != "done" {
		t.Fatalf("stranger on a floor %+v", event)
	}
}
