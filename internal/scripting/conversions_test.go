package scripting

import (
	"context"
	"testing"

	"maple-go-server/internal/content"
	bundled "maple-go-server/scripts"
)

type conversionHost struct {
	mapID    int64
	commands []Command
}

func TestEmbeddedSimpleNPCConversions(t *testing.T) {
	entries, err := bundled.SimpleNPCMigrations()
	if err != nil || len(entries) == 0 {
		t.Fatalf("simple NPC migration inventory: %v", err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Lua, func(t *testing.T) {
			source, err := bundled.Source(entry.Lua)
			if err != nil {
				t.Fatal(err)
			}
			registered, err := e.Register(entry.Lua, source)
			if err != nil || registered.SHA256 != entry.LuaSHA256 {
				t.Fatalf("reviewed source hash changed: %v", err)
			}
			host := &conversionHost{}
			session, event, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: host})
			if session != nil {
				defer session.Cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			switch entry.Kind {
			case "dialogue":
				if event.Kind != "prompt" || event.Prompt.Kind != "say" || event.Prompt.Text != entry.Text || event.Prompt.AllowBack || event.Prompt.AllowNext || len(host.commands) != 0 {
					t.Fatalf("literal message changed: %+v", event)
				}
				event, err = session.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "say", Finish: true})
				if err != nil || event.Kind != "done" || len(host.commands) != 0 {
					t.Fatalf("finish: %+v %v %+v", event, err, host.commands)
				}
			case "warp":
				if event.Kind != "done" || len(host.commands) != 1 {
					t.Fatalf("warp: %+v %+v", event, host.commands)
				}
				command := host.commands[0]
				if command.Kind != "warp" || len(command.Ints) != 2 || command.Ints[0] != int64(entry.MapID) || command.Ints[1] != int64(entry.PortalID) {
					t.Fatalf("warp destination changed: %+v", command)
				}
			default:
				t.Fatalf("unknown migration kind: %q", entry.Kind)
			}
		})
	}
}

func TestEmbeddedServiceNPCConversions(t *testing.T) {
	entries, err := bundled.ServiceMigrations()
	if err != nil || len(entries) == 0 {
		t.Fatalf("service migration inventory: %v", err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Lua, func(t *testing.T) {
			source, err := bundled.Source(entry.Lua)
			if err != nil {
				t.Fatal(err)
			}
			registered, err := e.Register(entry.Lua, source)
			if err != nil || registered.SHA256 != entry.LuaSHA256 {
				t.Fatalf("reviewed source hash changed: %v", err)
			}
			host := &conversionHost{}
			session, event, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: host})
			if session != nil {
				defer session.Cancel()
			}
			if err != nil || event.Kind != "done" || len(host.commands) != 1 {
				t.Fatalf("service open: %+v %v %+v", event, err, host.commands)
			}
			command := host.commands[0]
			if entry.Service == "storage" {
				if command.Kind != "open_storage" || len(command.Ints) != 0 {
					t.Fatalf("storage action changed: %+v", command)
				}
			} else {
				if command.Kind != "open_shop" || len(command.Ints) != 1 || command.Ints[0] != int64(entry.ShopID) {
					t.Fatalf("shop action changed: %+v", command)
				}
				if _, err := content.NPCShop(entry.ShopID); err != nil {
					t.Fatalf("converted shop missing from content: %v", err)
				}
			}
		})
	}
}

func TestEmbeddedPortalConversions(t *testing.T) {
	entries, err := bundled.PortalMigrations()
	if err != nil || len(entries) == 0 {
		t.Fatalf("portal migration inventory: %v", err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Lua, func(t *testing.T) {
			source, err := bundled.Source(entry.Lua)
			if err != nil {
				t.Fatal(err)
			}
			registered, err := e.Register(entry.Lua, source)
			if err != nil || registered.SHA256 != entry.LuaSHA256 {
				t.Fatalf("portal source hash: %v", err)
			}
			host := &conversionHost{}
			session, event, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: host, API: "portal"})
			if session != nil {
				defer session.Cancel()
			}
			if err != nil || event.Kind != "done" || len(host.commands) != 1 {
				t.Fatalf("portal run: %+v %v %+v", event, err, host.commands)
			}
			command := host.commands[0]
			if entry.PortalName != "" {
				if command.Kind != "warp" || len(command.Ints) != 1 || command.Ints[0] != int64(entry.MapID) || command.Text != entry.PortalName {
					t.Fatalf("named portal destination changed: %+v", command)
				}
			} else if command.Kind != "warp" || len(command.Ints) != 2 || command.Ints[0] != int64(entry.MapID) || command.Ints[1] != int64(entry.PortalID) || command.Text != "" {
				t.Fatalf("portal destination changed: %+v", command)
			}
		})
	}
}

func (h *conversionHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	h.commands = append(h.commands, c)
	switch c.Kind {
	case "map_id":
		return CommandResult{Value: h.mapID}, nil
	case "level":
		return CommandResult{Value: int64(1)}, nil
	}
	return CommandResult{Value: true}, nil
}

func TestEmbeddedLegacyNPCConversions(t *testing.T) {
	manifest, err := bundled.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for name, hash := range manifest {
		source, err := bundled.Source(name)
		if err != nil {
			t.Fatal(err)
		}
		script, err := e.Register(name, source)
		if err != nil || script.SHA256 != hash {
			t.Fatalf("register%s: %v", name, err)
		}
	}
	t.Run("2100 back navigation", func(t *testing.T) {
		host := &conversionHost{mapID: 10000}
		s, event, err := e.Start(context.Background(), testIdentity, "npc/2100.lua", StartOptions{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel()
		first := event.Prompt.Text
		event, err = answer(s, event, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !event.Prompt.AllowBack || event.Prompt.AllowNext {
			t.Fatal("legacy previous-only page lost")
		}
		event, err = s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "say", Back: true})
		if err != nil || event.Prompt.Text != first {
			t.Fatalf("return to first page=%+v %v", event, err)
		}
	})
	t.Run("2100 native finish", func(t *testing.T) {
		host := &conversionHost{mapID: 10000}
		s, event, err := e.Start(context.Background(), testIdentity, "npc/2100.lua", StartOptions{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel()
		event, err = answer(s, event, true, 0)
		if err != nil {
			t.Fatal(err)
		}
		event, err = s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "say", Finish: true})
		if err != nil || event.Kind != "done" {
			t.Fatalf("finish=%+v %v", event, err)
		}
	})
	t.Run("2101 declined", func(t *testing.T) {
		host := &conversionHost{}
		s, event, err := e.Start(context.Background(), testIdentity, "npc/2101.lua", StartOptions{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel()
		event, err = answer(s, event, false, 0)
		if err != nil || event.Prompt.Kind != "say" {
			t.Fatal(err)
		}
		event, err = answer(s, event, false, 0)
		if err != nil || event.Kind != "done" || len(host.commands) != 0 {
			t.Fatalf("decline mutated world %v %+v", err, host.commands)
		}
	})
	t.Run("2101 accepts original destination", func(t *testing.T) {
		host := &conversionHost{}
		s, event, err := e.Start(context.Background(), testIdentity, "npc/2101.lua", StartOptions{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel()
		event, err = answer(s, event, true, 0)
		if err != nil {
			t.Fatal(err)
		}
		event, err = answer(s, event, false, 0)
		if err != nil || event.Kind != "done" || len(host.commands) != 1 || host.commands[0].Kind != "warp" || host.commands[0].Ints[0] != 3 {
			t.Fatalf("legacy warp changed %v %+v", err, host.commands)
		}
	})
	t.Run("2007 original menu and destination", func(t *testing.T) {
		host := &conversionHost{mapID: 10000}
		s, event, err := e.Start(context.Background(), testIdentity, "npc/2007.lua", StartOptions{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel()
		if event.Prompt.Kind != "menu" || event.Prompt.Choices[0].ID != 3 {
			t.Fatal("legacy menu changed")
		}
		event, err = answer(s, event, false, 3)
		if err != nil || event.Kind != "done" {
			t.Fatal(err)
		}
		last := host.commands[len(host.commands)-1]
		if last.Kind != "warp" || last.Ints[0] != 104000000 {
			t.Fatalf("destination=%+v", last)
		}
	})
}
