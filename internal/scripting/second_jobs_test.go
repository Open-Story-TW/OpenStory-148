package scripting

import (
	"context"
	"fmt"
	"maple-go-server/scripts"
	"testing"
)

type secondJobHost struct {
	job, level int64
	advances   int
}

func (h *secondJobHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch c.Kind {
	case "job":
		return CommandResult{Value: h.job}, nil
	case "level":
		return CommandResult{Value: h.level}, nil
	case "advance_second_job":
		if c.Script != "npc/1052001.lua" || len(c.Ints) != 1 || c.Ints[0] != 130 || c.ID == "" {
			return CommandResult{}, fmt.Errorf("unbound second job %+v", c)
		}
		h.advances++
		h.job = 130
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected %s", c.Kind)
}
func TestSecondJobLuaRequiresTwoConfirmationsAndResumesFromActualJob(t *testing.T) {
	for _, cancelAt := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(cancelAt), func(t *testing.T) {
			engine, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			source, _ := scripts.Source("npc/1052001.lua")
			if _, err = engine.Register("npc/1052001.lua", source); err != nil {
				t.Fatal(err)
			}
			host := &secondJobHost{job: 100, level: 30}
			session, event, err := engine.Start(context.Background(), testIdentity, "npc/1052001.lua", StartOptions{Host: host})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Cancel()
			confirmations := 0
			for event.Kind == "prompt" {
				if host.advances != 0 {
					t.Fatal("effect before final consent")
				}
				if event.Prompt.Kind == "yes_no" {
					confirmations++
				}
				accept := cancelAt == 0 || confirmations != cancelAt
				event, err = answerFirstJob(session, event, accept, 130)
				if err != nil {
					t.Fatal(err)
				}
			}
			if cancelAt == 0 && (host.advances != 1 || host.job != 130 || confirmations != 2) {
				t.Fatal("missing exact advancement")
			}
			if cancelAt != 0 && host.advances != 0 {
				t.Fatal("cancel advanced")
			}
		})
	}
	for _, tc := range []struct{ job, level int64 }{{100, 29}, {130, 30}} {
		engine, _ := New(Options{})
		source, _ := scripts.Source("npc/1052001.lua")
		_, _ = engine.Register("npc/1052001.lua", source)
		host := &secondJobHost{job: tc.job, level: tc.level}
		session, event, err := engine.Start(context.Background(), testIdentity, "npc/1052001.lua", StartOptions{Host: host})
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "prompt" {
			_, err = answerFirstJob(session, event, true, 130)
		}
		session.Cancel()
		if err != nil || host.advances != 0 {
			t.Fatal("restart/premature repeated change", err)
		}
	}
}

func TestSecondJobLuaEndBeforeFinalConfirmationHasNoEffect(t *testing.T) {
	engine, _ := New(Options{})
	source, _ := scripts.Source("npc/1052001.lua")
	_, _ = engine.Register("npc/1052001.lua", source)
	host := &secondJobHost{job: 100, level: 30}
	session, event, err := engine.Start(context.Background(), testIdentity, "npc/1052001.lua", StartOptions{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	event, err = answerFirstJob(session, event, true, 130)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != "prompt" || event.Prompt.Kind != "menu" {
		t.Fatal("missing original selection")
	}
	session.Cancel()
	if _, err = answerFirstJob(session, event, true, 130); err == nil || host.advances != 0 {
		t.Fatal("End resumed advancement", err)
	}
}
