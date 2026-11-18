package scripting

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
)

func TestAzwanSweepAdmissionDisplaysDailyCounter(t *testing.T) {
	host := azwanAdmissionHost{entries: 3}
	session, event := startNPC(t, 2183003, host)
	if event.Prompt == nil || len(event.Prompt.Choices) != 2 || !strings.Contains(event.Prompt.Choices[0].Text, "剩餘 7 次") {
		t.Fatalf("entry prompt %+v", event)
	}
	if event = respond(t, session, event, Response{Choice: 1}); event.Kind != "done" {
		t.Fatalf("entry response %+v", event)
	}
}

type azwanAdmissionHost struct{ entries int64 }

func (h azwanAdmissionHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch c.Kind {
	case "map_id":
		return CommandResult{Value: int64(262000000)}, nil
	case "read_log":
		if c.Log != nil && *c.Log == (domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogDaily, Name: "azwan_sweep_entries"}) {
			return CommandResult{Value: h.entries}, nil
		}
	case "event_start":
		if c.Text == "AzwanSweep" {
			return CommandResult{Value: map[string]any{"ok": true}}, nil
		}
	}
	return CommandResult{}, fmt.Errorf("unexpected Azwan command %+v", c)
}
