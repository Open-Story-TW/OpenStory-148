package scripting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/scripts"
)

type firstJobsHost struct {
	job, level, target int64
	advances           int
}

func answerFirstJob(s *Session, event Event, accept bool, choice int64) (Event, error) {
	if event.Prompt.Kind == "say" && !event.Prompt.AllowNext {
		return s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "say", Finish: true})
	}
	return answer(s, event, accept, choice)
}

func (h *firstJobsHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch c.Kind {
	case "job":
		return CommandResult{Value: h.job}, nil
	case "level":
		return CommandResult{Value: h.level}, nil
	case "advance_first_job":
		if len(c.Ints) != 1 || c.Ints[0] != h.target || c.ID == "" || c.Script == "" {
			return CommandResult{}, fmt.Errorf("invalid typed advancement: %+v", c)
		}
		h.advances++
		h.job = h.target
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}

func TestFirstJobLuaManifestsPinAuditedSources(t *testing.T) {
	entries, err := scripts.FirstJobMigrations()
	if err != nil || len(entries) != 5 {
		t.Fatalf("first-job manifest: %v", err)
	}
	for _, entry := range entries {
		source, err := scripts.Source(entry.Lua)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(source)
		if entry.LuaSHA256 != hex.EncodeToString(h[:]) || entry.Scope != "ordinary-first-job-only" || entry.NativeMCP != "pending" {
			t.Fatalf("unpinned or overclaimed branch: %+v", entry)
		}
		legacy, err := os.ReadFile(filepath.Join("..", "..", "..", "MapleStory_Legacy_Server", "scripts", entry.Legacy))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		h = sha256.Sum256(legacy)
		if entry.SourceSHA256 != hex.EncodeToString(h[:]) {
			t.Fatalf("legacy changed since review: %s", entry.Legacy)
		}
	}
}

func TestFirstJobLuaRequiresEveryConfirmationAndRecoversFromJob(t *testing.T) {
	entries, _ := scripts.FirstJobMigrations()
	for _, entry := range entries {
		if entry.NPCID == 1022000 {
			continue
		} // Existing typed warrior compatibility tests.
		for _, target := range entry.Jobs {
			for _, accept := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%d/%v", entry.NPCID, target, accept), func(t *testing.T) {
					e, err := New(Options{})
					if err != nil {
						t.Fatal(err)
					}
					source, _ := scripts.Source(entry.Lua)
					if _, err = e.Register(entry.Lua, source); err != nil {
						t.Fatal(err)
					}
					host := &firstJobsHost{level: int64(entry.MinimumLevel), target: int64(target)}
					s, event, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: host})
					if err != nil {
						t.Fatal(err)
					}
					defer s.Cancel()
					confirmed := 0
					for count := 0; event.Kind == "prompt" && count < 20; count++ {
						choice := int64(0)
						if entry.NPCID == 1052001 {
							choice = int64(target)
						}
						if event.Prompt.Kind == "yes_no" {
							if host.advances != 0 {
								t.Fatal("effect committed before consent")
							}
							confirmed++
						}
						forward := accept || event.Prompt.Kind != "yes_no"
						if event.Prompt.Kind == "say" {
							forward = event.Prompt.AllowNext
						}
						event, err = answerFirstJob(s, event, forward, choice)
						if err != nil {
							t.Fatal(err)
						}
						if host.advances > 0 {
							break
						}
					}
					if (host.advances == 1) != accept {
						t.Fatalf("accept=%v effects=%d event=%+v", accept, host.advances, event)
					}
					if accept && entry.NPCID == 1052001 && confirmed != 2 {
						t.Fatal("generic source bypassed second confirmation")
					}
					s.Cancel() // Disconnect after durable command, before remaining dialogue.
					if accept {
						next, event, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: host})
						if err != nil || event.Kind != "prompt" || host.advances != 1 {
							t.Fatalf("restart repeated effect: %+v %v", event, err)
						}
						next.Cancel()
					}
				})
			}
		}
	}
}

func TestFirstJobLuaLevelGateNeverCommits(t *testing.T) {
	entries, _ := scripts.FirstJobMigrations()
	for _, entry := range entries {
		if entry.NPCID == 1022000 {
			continue
		}
		e, _ := New(Options{})
		source, _ := scripts.Source(entry.Lua)
		_, _ = e.Register(entry.Lua, source)
		h := &firstJobsHost{level: int64(entry.MinimumLevel - 1)}
		s, event, err := e.Start(context.Background(), testIdentity, entry.Lua, StartOptions{Host: h})
		if err != nil {
			t.Fatal(err)
		}
		for count := 0; event.Kind == "prompt" && count < 10; count++ {
			forward := event.Prompt.Kind != "say" || event.Prompt.AllowNext
			event, err = answerFirstJob(s, event, forward, 0)
			if err != nil {
				t.Fatal(err)
			}
		}
		if h.advances != 0 || event.Kind != "done" {
			t.Fatalf("below-level branch advanced: %d %+v", entry.NPCID, event)
		}
		s.Cancel()
	}
}
