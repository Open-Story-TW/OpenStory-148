package scripting

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/scripts"
)

type mapTestHost struct {
	gender   int64
	commands []Command
}

func (h *mapTestHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	h.commands = append(h.commands, c)
	if c.Kind == "gender" {
		return CommandResult{Value: h.gender}, nil
	}
	if c.Kind == "play_scene" {
		return CommandResult{Value: true}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected %s", c.Kind)
}
func TestMapEntryIsSequentialLuaWithGenderBoundScene(t *testing.T) {
	source, err := scripts.Source("map/on_user_enter/goLith.lua")
	if err != nil {
		t.Fatal(err)
	}
	for _, gender := range []int64{0, 1} {
		h := &mapTestHost{gender: gender}
		e, _ := New(Options{Host: h})
		_, err = e.Register("map/on_user_enter/goLith.lua", source)
		if err != nil {
			t.Fatal(err)
		}
		s, event, err := e.Start(context.Background(), testIdentity, "map/on_user_enter/goLith.lua", StartOptions{API: "map"})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel()
		if event.Kind != "done" || len(h.commands) != 2 || h.commands[0].Kind != "gender" || h.commands[1].Kind != "play_scene" || h.commands[1].Text != fmt.Sprintf("Effect/Direction3.img/goLith/Scene%d", gender) {
			t.Fatalf("event=%+v commands=%+v", event, h.commands)
		}
	}
}

func TestMapEntryBreakpointShowsLocalsBeforeSceneEffect(t *testing.T) {
	source, err := scripts.Source("map/on_user_enter/goLith.lua")
	if err != nil {
		t.Fatal(err)
	}
	h := &mapTestHost{gender: 1}
	e, _ := New(Options{Host: h})
	if _, err = e.Register("map/on_user_enter/goLith.lua", source); err != nil {
		t.Fatal(err)
	}
	s, event, err := e.Start(context.Background(), testIdentity, "map/on_user_enter/goLith.lua", StartOptions{API: "map", Breakpoints: []int{5}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if event.Kind != "paused" || len(h.commands) != 1 || h.commands[0].Kind != "gender" {
		t.Fatalf("scene ran before breakpoint: %+v %+v", event, h.commands)
	}
	locals, err := s.Inspect(testIdentity)
	if err != nil || locals.Locals["gender"] != int64(1) {
		t.Fatalf("locals %+v %v", locals, err)
	}
	event, err = s.Continue(context.Background(), testIdentity)
	if err != nil || event.Kind != "done" || len(h.commands) != 2 || h.commands[1].Text != "Effect/Direction3.img/goLith/Scene1" {
		t.Fatalf("continue %+v %+v %v", event, h.commands, err)
	}
}
