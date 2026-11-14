package scripting

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var testIdentity = Identity{SessionID: "test-auth-session", CharacterID: 1, Generation: 3}

func engineFor(t *testing.T, source string, options Options) (*Engine, Script) {
	t.Helper()
	e, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	script, err := e.Register("npc/test.lua", []byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return e, script
}
func answer(s *Session, e Event, value bool, choice int64) (Event, error) {
	return s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: e.Prompt.ID, Kind: e.Prompt.Kind, Bool: value, Choice: choice})
}

func TestSequentialDialogueRetainsLocalsAndRejectsStaleResponses(t *testing.T) {
	e, _ := engineFor(t, `local count = 41
npc:say("first")
count = count + 1
local accepted = npc:ask_yes_no("continue?")
if accepted then npc:say(tostring(count)) end`, Options{})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if event.Kind != "prompt" || event.Prompt.Text != "first" {
		t.Fatalf("event=%+v", event)
	}
	old := event
	event, err = answer(s, event, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if event.Prompt.Kind != "yes_no" {
		t.Fatalf("event=%+v", event)
	}
	if _, err = answer(s, old, true, 0); !errors.Is(err, ErrStalePrompt) {
		t.Fatalf("duplicate response: %v", err)
	}
	event, err = answer(s, event, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if event.Prompt.Text != "42" {
		t.Fatalf("local count not preserved: %+v", event)
	}
	event, err = answer(s, event, false, 0)
	if err != nil || event.Kind != "done" {
		t.Fatalf("done=%+v err=%v", event, err)
	}
}

func TestMenuAndIdentityPinning(t *testing.T) {
	e, oldScript := engineFor(t, `local result=npc:menu("pick",{{id=10,text="A"},{id=20,text="B"}})
npc:say(tostring(result))`, Options{})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	wrong := testIdentity
	wrong.Generation++
	if _, err = s.Respond(context.Background(), Response{Identity: wrong, PromptID: event.Prompt.ID, Kind: "menu", Choice: 10}); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
	if _, err = answer(s, event, false, 30); err == nil {
		t.Fatal("accepted unoffered menu choice")
	}
	newScript, err := e.Register("npc/test.lua", []byte(`npc:say("new source")`))
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := s.Source(testIdentity)
	if err != nil || !strings.Contains(pinned, `local result=npc:menu`) || strings.Contains(pinned, "new source") {
		t.Fatalf("debug source did not retain the invocation snapshot: %q %v", pinned, err)
	}
	next, err := answer(s, event, false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if next.SHA256 != oldScript.SHA256 || next.Prompt.Text != "20" || next.SHA256 == newScript.SHA256 {
		t.Fatal("active script was hot-reloaded")
	}
	second, newEvent, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	if newEvent.SHA256 != newScript.SHA256 || newEvent.Prompt.Text != "new source" {
		t.Fatal("new session did not get new source")
	}
}

func TestDebuggerBreakpointsStepAndReadOnlyLocals(t *testing.T) {
	e, _ := engineFor(t, "local value=7\nvalue=value+1\nvalue=value+2\nnpc:say(tostring(value))\n", Options{})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{Breakpoints: []int{3}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if event.Kind != "paused" || event.Debug.Line != 3 || event.Debug.Locals["value"] != int64(8) {
		t.Fatalf("breakpoint=%+v", event)
	}
	locals, err := s.Inspect(testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	locals.Locals["value"] = int64(900)
	checked, _ := s.Inspect(testIdentity)
	if checked.Locals["value"] != int64(8) {
		t.Fatal("inspection modified VM state")
	}
	event, err = s.Step(context.Background(), testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != "paused" || event.Debug.Line != 4 || event.Debug.Locals["value"] != int64(10) {
		t.Fatalf("step=%+v", event)
	}
	event, err = s.Continue(context.Background(), testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != "prompt" || event.Prompt.Text != "10" {
		t.Fatalf("continue=%+v", event)
	}
}

func TestDebuggerSeesLoopAndFunctionLocals(t *testing.T) {
	source := "local outer=5\nlocal function f(parameter)\n  local inner=parameter+1\n  npc:say(tostring(inner))\nend\nfor index=1,2 do\n  f(index+outer)\nend"
	e, _ := engineFor(t, source, Options{})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{Breakpoints: []int{4}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if event.Kind != "paused" || event.Debug.Locals["outer"] != int64(5) || event.Debug.Locals["parameter"] != int64(6) || event.Debug.Locals["inner"] != int64(7) {
		t.Fatalf("function locals=%+v", event)
	}
}

func TestDebuggerUserGlobalsAreReadOnlyAndExcludeRuntime(t *testing.T) {
	e, _ := engineFor(t, "globalCount = 7\nlocal next = globalCount + 1\nnpc:say(tostring(next))", Options{})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{Breakpoints: []int{3}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if event.Kind != "paused" || event.Debug.Locals["next"] != int64(8) || event.Debug.Globals["globalCount"] != int64(7) || len(event.Debug.Globals) != 1 {
		t.Fatalf("global snapshot: %+v", event.Debug)
	}
	event.Debug.Globals["globalCount"] = int64(900)
	copy, err := s.Inspect(testIdentity)
	if err != nil || copy.Globals["globalCount"] != int64(7) {
		t.Fatalf("inspector changed globals: %+v %v", copy, err)
	}
}

func TestVMBudgetsAndLibraryRestrictions(t *testing.T) {
	for _, source := range []string{`while true do end`, `local values={} for i=1,1000000 do values[i]=string.rep("x",1024) end`} {
		limits := DefaultLimits()
		limits.MemoryBytes = 2 << 20
		limits.StepCPU = 1000
		e, _ := engineFor(t, source, Options{Limits: limits})
		started := time.Now()
		s, _, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
		if s != nil {
			s.Cancel()
		}
		if err == nil {
			t.Fatal("unbounded script did not hit quota")
		}
		if time.Since(started) > 2*time.Second {
			t.Fatal("quota termination took too long")
		}
	}
	e, _ := engineFor(t, `assert(io==nil and os==nil and package==nil and require==nil and debug==nil and load==nil and dofile==nil and loadfile==nil and Java==nil and collectgarbage==nil)
npc:say(_VERSION)`, Options{})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if event.Prompt.Text != "Golua 5.5" {
		t.Fatalf("unexpected language version %q", event.Prompt.Text)
	}
}

func TestVMMemoryBudgetFailsBeforeLargeAllocation(t *testing.T) {
	limits := DefaultLimits()
	limits.MemoryBytes = 2 << 20
	e, _ := engineFor(t, `local blob=string.rep("x",8388608) npc:say(tostring(#blob))`, Options{Limits: limits})
	s, _, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if s != nil {
		s.Cancel()
	}
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "memory") {
		t.Fatalf("wanted memory quota, got %v", err)
	}
}

func TestBackIsExplicitAndCannotCrossMutation(t *testing.T) {
	e, _ := engineFor(t, `local forward=npc:say("page",true,false)
assert(forward==false)
npc:say("back accepted")`, Options{})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	if _, err = answer(s, event, false, 0); err == nil {
		t.Fatal("accepted next on previous-only page")
	}
	event, err = s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "say", Back: true})
	if err != nil || event.Prompt.Text != "back accepted" {
		t.Fatalf("back err=%v event=%+v", err, event)
	}
}

func TestBooleanDialogueResultIsVisibleAtFollowingBreakpoint(t *testing.T) {
	for _, kind := range []string{"say", "yes_no"} {
		t.Run(kind, func(t *testing.T) {
			call := `npc:say("Next or Previous",true,true)`
			if kind == "yes_no" {
				call = `npc:ask_yes_no("Yes or No")`
			}
			engine, _ := engineFor(t, "local forward="+call+"\nlocal branch=forward and 'next' or 'back'\nnpc:say(branch)", Options{})
			session, event, err := engine.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{Breakpoints: []int{2}})
			if err != nil || event.Kind != "prompt" {
				t.Fatalf("initial prompt: %+v %v", event, err)
			}
			defer session.Cancel()
			event, err = session.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: kind, Back: kind == "say", Bool: false})
			if err != nil || event.Kind != "paused" || event.Debug.Line != 2 || event.Debug.Locals["forward"] != false {
				t.Fatalf("dialogue result at breakpoint: %+v %v", event, err)
			}
			event, err = session.Continue(context.Background(), testIdentity)
			if err != nil || event.Kind != "prompt" || event.Prompt.Text != "back" {
				t.Fatalf("boolean branch: %+v %v", event, err)
			}
		})
	}
}

type recordingHost struct {
	mu       sync.Mutex
	commands []Command
}

func (h *recordingHost) Execute(ctx context.Context, id Identity, c Command) (CommandResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id != testIdentity {
		return CommandResult{}, ErrIdentity
	}
	h.commands = append(h.commands, c)
	if c.Kind == "read_log" {
		return CommandResult{Value: int64(0)}, nil
	}
	return CommandResult{Value: true}, nil
}
func TestTypedExchangeAndLogCommands(t *testing.T) {
	host := &recordingHost{}
	e, _ := engineFor(t, `local count=npc:log("character","day","daily-gift")
if count==0 and npc:ask_yes_no("claim?") then
 npc:exchange({mesos=10,items={{id=2000000,quantity=1}},logs={{scope="character",period="day",name="daily-gift",delta=1,expected=0,maximum=1}}})
end`, Options{Host: host})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel()
	event, err = answer(s, event, true, 0)
	if err != nil || event.Kind != "done" {
		t.Fatalf("exchange err=%v event=%+v", err, event)
	}
	if len(host.commands) != 2 {
		t.Fatalf("commands=%+v", host.commands)
	}
	cmd := host.commands[1]
	if cmd.Kind != "exchange" || cmd.Exchange.MesosDelta != 10 || cmd.Exchange.Items[0].Quantity != 1 || *cmd.Exchange.Logs[0].Expected != 0 || cmd.ID == "" {
		t.Fatalf("bad command=%+v", cmd)
	}
}

func TestTypedQuestDataInAtomicExchange(t *testing.T) {
	host := &recordingHost{}
	e, _ := engineFor(t, `local prior = npc:quest_data(700001)
npc:exchange({quest_data={{id=700001,data="rewarded:page2",expected=""}},items={{id=2000000,quantity=1}}})`, Options{Host: host})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if s != nil {
		defer s.Cancel()
	}
	if err != nil || event.Kind != "done" || len(host.commands) != 2 {
		t.Fatalf("quest data program: %+v %v", event, err)
	}
	if host.commands[0].Kind != "quest_data" || host.commands[0].Ints[0] != 700001 {
		t.Fatalf("quest data lookup changed: %+v", host.commands[0])
	}
	change := host.commands[1].Exchange.QuestData[0]
	if change.QuestID != 700001 || change.Data != "rewarded:page2" || change.Expected == nil || *change.Expected != "" {
		t.Fatalf("quest data condition changed: %+v", change)
	}
}

func TestCancelAndPromptTimeout(t *testing.T) {
	limits := DefaultLimits()
	limits.PromptTimeout = time.Millisecond
	e, _ := engineFor(t, `npc:say("waiting")`, Options{Limits: limits})
	s, event, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if _, err = answer(s, event, false, 0); !errors.Is(err, ErrTimeout) {
		t.Fatalf("expired reply=%v", err)
	}
	s, event, err = e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.Cancel()
	if _, err = answer(s, event, false, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("cancel reply=%v", err)
	}
	if _, err = s.CurrentEvent(testIdentity); !errors.Is(err, ErrClosed) {
		t.Fatalf("cancelled session still appears active: %v", err)
	}
	if _, err = s.Source(testIdentity); !errors.Is(err, ErrClosed) {
		t.Fatalf("cancelled source still accessible: %v", err)
	}
}

func TestSourceAndHostLimits(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape.lua", "/absolute.lua", "npc/no.js"} {
		if _, err = e.Register(name, []byte("return")); err == nil {
			t.Fatal(name)
		}
	}
	if _, err = e.Register("npc/unsafe.lua", []byte(`local __maple_trace=nil`)); err == nil {
		t.Fatal("private debugger identifier accepted")
	}
	_, _ = e.Register("npc/host.lua", []byte(`npc:quest_state(1)`))
	s, _, err := e.Start(context.Background(), testIdentity, "npc/host.lua", StartOptions{})
	if s != nil {
		s.Cancel()
	}
	if err == nil {
		t.Fatal("missing host capability silently succeeded")
	}
}

func TestQuotaCannotBeCaughtAndOtherSessionKeepsRunning(t *testing.T) {
	e, _ := engineFor(t, `npc:say("ready") while true do pcall(function() while true do end end) end`, Options{})
	bad, prompt, err := e.Start(context.Background(), testIdentity, "npc/test.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Cancel()
	_, _ = e.Register("npc/good.lua", []byte(`npc:say("one") npc:say("two")`))
	good, event, err := e.Start(context.Background(), testIdentity, "npc/good.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer good.Cancel()
	if _, err = answer(bad, prompt, false, 0); err == nil {
		t.Fatal("pcall swallowed hard quota")
	}
	event, err = answer(good, event, false, 0)
	if err != nil || event.Prompt.Text != "two" {
		t.Fatalf("one script failure affected another: %+v %v", event, err)
	}
}
