package world

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maple-go-server/internal/platform"
	"maple-go-server/internal/scripting"
	"maple-go-server/scripts"
)

func writeScript(t *testing.T, dir, name, source string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func reloadServer(t *testing.T) (*Server, string) {
	t.Helper()
	world, err := newScriptWorld()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := &Server{cfg: platform.Config{ScriptsDir: dir, RPCToken: strings.Repeat("t", 32)}, scripts: world}
	// The overlay is package state: leave the embedded sources behind.
	t.Cleanup(func() {
		s.cfg.ScriptsDir = t.TempDir()
		if _, err := s.reloadScripts(); err != nil {
			t.Error(err)
		}
	})
	return s, dir
}

func firstSay(t *testing.T, s *Server, name string) (*scripting.Session, string) {
	t.Helper()
	id := scripting.Identity{SessionID: "reload-test", CharacterID: 1, Generation: 1}
	session, event, err := s.scripts.engine.Start(context.Background(), id, name, scripting.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(session.Cancel)
	if err != nil || event.Prompt == nil {
		t.Fatalf("%s: no prompt: %+v", name, event)
	}
	return session, event.Prompt.Text
}

func TestScriptReloadAddsEditsAndRemovesSources(t *testing.T) {
	s, dir := reloadServer(t)
	const added, shadowed = "npc/9999901.lua", "npc/1012100.lua"
	embedded, err := scripts.Source(shadowed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scripts.Source(added); err == nil {
		t.Fatal("test script already exists")
	}
	writeScript(t, dir, added, "npc:say(\"first\", false, true)\nnpc:say(\"first again\", false, false)\n")
	writeScript(t, dir, shadowed, "npc:say(\"from disk\", false, false)\r\n")
	report, err := s.reloadScripts()
	if err != nil || len(report.Changed) != 2 || len(report.Removed) != 0 || len(report.Rejected) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	open, text := firstSay(t, s, added)
	if text != "first" {
		t.Fatal(text)
	}
	if _, text := firstSay(t, s, shadowed); text != "from disk" {
		t.Fatal(text)
	}

	// An edit reaches the next conversation; the open one keeps its source.
	writeScript(t, dir, added, "npc:say(\"second\", false, false)\n")
	if report, err = s.reloadScripts(); err != nil || len(report.Changed) != 1 || report.Changed[0] != added {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, text := firstSay(t, s, added); text != "second" {
		t.Fatal(text)
	}
	event, err := open.Respond(context.Background(), scripting.Response{Identity: scripting.Identity{SessionID: "reload-test", CharacterID: 1, Generation: 1}, PromptID: promptID(t, open), Kind: "say"})
	if err != nil || event.Prompt == nil || event.Prompt.Text != "first again" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	if report, err = s.reloadScripts(); err != nil || len(report.Changed)+len(report.Removed)+len(report.Rejected) != 0 {
		t.Fatalf("unchanged directory: report=%+v err=%v", report, err)
	}

	// Deleting the files drops the new script and restores the built-in one.
	for _, name := range []string{added, shadowed} {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	if report, err = s.reloadScripts(); err != nil || len(report.Changed) != 1 || report.Changed[0] != shadowed || len(report.Removed) != 1 || report.Removed[0] != added {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, err := scripts.Source(added); err == nil {
		t.Fatal("removed script still has a source")
	}
	id := scripting.Identity{SessionID: "reload-test", CharacterID: 1, Generation: 1}
	if _, _, err := s.scripts.engine.Start(context.Background(), id, added, scripting.StartOptions{}); err == nil {
		t.Fatal("removed script still starts")
	}
	if now, _ := scripts.Source(shadowed); !bytes.Equal(now, embedded) {
		t.Fatal("built-in source was not restored")
	}
}

func promptID(t *testing.T, session *scripting.Session) string {
	t.Helper()
	event, err := session.CurrentEvent(scripting.Identity{SessionID: "reload-test", CharacterID: 1, Generation: 1})
	if err != nil || event.Prompt == nil {
		t.Fatal("no open prompt")
	}
	return event.Prompt.ID
}

func TestScriptReloadKeepsRunningSourceWhenNewOneIsBroken(t *testing.T) {
	s, dir := reloadServer(t)
	const name = "npc/9999902.lua"
	writeScript(t, dir, name, "npc:say(\"good\", false, false)\n")
	if report, err := s.reloadScripts(); err != nil || len(report.Changed) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	writeScript(t, dir, name, "npc:say(\"broken\", false, false\n")
	writeScript(t, dir, "npc/9999903.lua", "if then end\n")
	report, err := s.reloadScripts()
	if err != nil || len(report.Changed) != 0 || len(report.Removed) != 0 || len(report.Rejected) != 2 || report.Rejected[name] == "" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, text := firstSay(t, s, name); text != "good" {
		t.Fatal(text)
	}
	if source, _ := scripts.Source(name); !strings.Contains(string(source), "good") {
		t.Fatalf("source=%q", source)
	}
	if _, err := scripts.Source("npc/9999903.lua"); err == nil {
		t.Fatal("a rejected new script has a source")
	}
	// Fixing the file is picked up by the next reload.
	writeScript(t, dir, name, "npc:say(\"fixed\", false, false)\n")
	if report, err = s.reloadScripts(); err != nil || len(report.Changed) != 1 || len(report.Rejected) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if _, text := firstSay(t, s, name); text != "fixed" {
		t.Fatal(text)
	}
}

func TestScriptReloadDriverAppliesToGeneratedSourcesOrIsRejected(t *testing.T) {
	s, dir := reloadServer(t)
	migrations, err := scripts.LegacyQuestMigrations()
	if err != nil || len(migrations) == 0 {
		t.Fatalf("migrations=%d err=%v", len(migrations), err)
	}
	quest := migrations[0].Lua
	before, _ := scripts.Source(quest)
	driver := string(scripts.LegacyQuestDriver())

	writeScript(t, dir, "legacy/quest_driver.lua", driver+"\nlocal function (\n")
	report, err := s.reloadScripts()
	if err != nil || len(report.Changed) != 0 || report.Rejected["legacy/quest_driver.lua"] == "" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if now, _ := scripts.Source(quest); !bytes.Equal(now, before) {
		t.Fatal("a rejected driver changed a generated source")
	}

	writeScript(t, dir, "legacy/quest_driver.lua", driver+"\n-- reloaded driver\n")
	if report, err = s.reloadScripts(); err != nil || len(report.Changed) != len(migrations) || len(report.Rejected) != 0 {
		t.Fatalf("changed=%d want=%d rejected=%+v err=%v", len(report.Changed), len(migrations), report.Rejected, err)
	}
	if now, _ := scripts.Source(quest); !strings.HasSuffix(string(now), "-- reloaded driver\n") {
		t.Fatal("the new driver was not appended")
	}
}

func TestScriptReloadEndpointNeedsTokenAndDirectory(t *testing.T) {
	s, dir := reloadServer(t)
	call := func(method, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/admin/scripts/reload", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.scriptReload(w, r)
		return w
	}
	if w := call(http.MethodPost, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(http.MethodGet, s.cfg.RPCToken); w.Code != 405 {
		t.Fatal(w.Code)
	}
	writeScript(t, dir, "npc/9999904.lua", "npc:say(\"hi\", false, false)\n")
	if w := call(http.MethodPost, s.cfg.RPCToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"changed":["npc/9999904.lua"]`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	s.cfg.ScriptsDir = ""
	if w := call(http.MethodPost, s.cfg.RPCToken); w.Code != 409 {
		t.Fatal(w.Code)
	}
	s.cfg.ScriptsDir = dir
}
