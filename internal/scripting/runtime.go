// Package scripting runs sequential Lua 5.5 dialogues in isolated bounded VMs.
package scripting

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arnodel/golua/lib"
	"github.com/arnodel/golua/lib/base"
	"github.com/arnodel/golua/lib/mathlib"
	"github.com/arnodel/golua/lib/packagelib"
	"github.com/arnodel/golua/lib/stringlib"
	"github.com/arnodel/golua/lib/tablelib"
	"github.com/arnodel/golua/lib/utf8lib"
	rt "github.com/arnodel/golua/runtime"
	"github.com/arnodel/golua/scanner"
	"github.com/arnodel/golua/token"
	"maple-go-server/internal/domain"
)

var (
	ErrIdentity    = errors.New("script session identity mismatch")
	ErrStalePrompt = errors.New("stale or duplicate script response")
	ErrNotPaused   = errors.New("script is not paused")
	ErrClosed      = errors.New("script session is closed")
	ErrTimeout     = errors.New("script session timed out")
	ErrCancelled   = errors.New("script session cancelled")
)

type Identity struct {
	SessionID   string `json:"session_id"`
	CharacterID int64  `json:"character_id"`
	Generation  int64  `json:"generation"`
}
type Command struct {
	ID       string                 `json:"id"`
	Script   string                 `json:"script"` // pinned source identity, assigned by the VM host bridge.
	Kind     string                 `json:"kind"`
	Ints     []int64                `json:"ints,omitempty"`
	Text     string                 `json:"text,omitempty"`
	Log      *domain.ScriptLogKey   `json:"log,omitempty"`
	Exchange *domain.ScriptExchange `json:"exchange,omitempty"`
	Line     int                    `json:"line,omitempty"` // source line of the call, for GM debug notices.
}
type CommandResult struct {
	Value any `json:"value,omitempty"`
}

// Host executes one bounded command. It must use the supplied context and must
// never keep a transaction open after Execute returns. Identity comes from the
// authenticated session, never a Lua argument. Durable commands use Command.ID.
type Host interface {
	Execute(context.Context, Identity, Command) (CommandResult, error)
}

type Limits struct {
	MaxSourceBytes int
	MemoryBytes    uint64
	TotalCPU       uint64
	StepCPU        uint64
	StepTime       time.Duration
	PromptTimeout  time.Duration
	SessionTimeout time.Duration
	MaxPrompts     int
	MaxCommands    int
}

func DefaultLimits() Limits {
	return Limits{MaxSourceBytes: 65536, MemoryBytes: 16 << 20, TotalCPU: 10000000, StepCPU: 100000, StepTime: 250 * time.Millisecond, PromptTimeout: 3 * time.Minute, SessionTimeout: 30 * time.Minute, MaxPrompts: 512, MaxCommands: 256}
}

type Options struct {
	Limits Limits
	Host   Host
}
type Script struct {
	Name   string
	SHA256 string
	source []byte
	// driverLine is the first line of a shared legacy driver appended to a
	// generated source (scripts/legacy), or 0. GM notices name the last line
	// executed above it, so they point at the converted Legacy statement.
	driverLine int
}

// legacyDriverHeader opens every shared driver in scripts/legacy.
const legacyDriverHeader = "-- Shared driver for generated Legacy "

func driverLine(source []byte) int {
	i := bytes.Index(source, []byte("\n"+legacyDriverHeader))
	if i < 0 {
		return 0
	}
	return bytes.Count(source[:i+1], []byte("\n")) + 1
}

type Engine struct {
	mu      sync.RWMutex
	scripts map[string]Script
	options Options
}

func New(options Options) (*Engine, error) {
	if !rt.QuotasAvailable {
		return nil, errors.New("Lua quotas were disabled at build time")
	}
	if options.Limits == (Limits{}) {
		options.Limits = DefaultLimits()
	}
	l := options.Limits
	if l.MaxSourceBytes < 1 || l.MemoryBytes < 65536 || l.TotalCPU < 1000 || l.StepCPU < 100 || l.StepTime < time.Millisecond || l.PromptTimeout <= 0 || l.SessionTimeout <= 0 || l.MaxPrompts < 1 || l.MaxCommands < 1 {
		return nil, errors.New("invalid script resource limits")
	}
	return &Engine{scripts: make(map[string]Script), options: options}, nil
}

var scriptName = regexp.MustCompile(`^[A-Za-z0-9_/.-]+\.lua$`)

func (e *Engine) scan(name string, source []byte) error {
	if !scriptName.MatchString(name) || strings.Contains(name, "..") || strings.HasPrefix(name, "/") || len(source) == 0 || len(source) > e.options.Limits.MaxSourceBytes {
		return errors.New("invalid Lua script name or size")
	}
	sc := scanner.New(name, source)
	for {
		t := sc.Scan()
		if t.Type == token.EOF {
			return nil
		}
		if t.Type == token.INVALID || t.Type == token.UNFINISHED {
			return errors.New("invalid Lua token")
		}
		if t.Type == token.IDENT && string(t.Lit) == "__maple_trace" {
			return errors.New("reserved debugger identifier")
		}
	}
}

// Check reports whether Register would take source and whether it compiles,
// without registering it. A reload uses it so that a source with a syntax
// error never replaces the one already running.
func (e *Engine) Check(name string, source []byte) (err error) {
	if err = e.scan(name, source); err != nil {
		return err
	}
	l := e.options.Limits
	vm := rt.New(io.Discard, rt.WithRuntimeContext(rt.RuntimeContextDef{HardLimits: rt.RuntimeResources{Cpu: l.TotalCPU, Memory: l.MemoryBytes}, RequiredFlags: rt.ComplyCpuSafe | rt.ComplyMemSafe | rt.ComplyTimeSafe | rt.ComplyIoSafe}))
	defer vm.Close(nil)
	defer func() {
		if value := recover(); value != nil {
			quota, ok := value.(rt.ContextTerminationError)
			if !ok {
				panic(value)
			}
			err = quota
		}
	}()
	_, err = vm.MainThread().CallContext(rt.RuntimeContextDef{}, func() error {
		_, e := compile(vm, name, source)
		return e
	})
	return err
}

// Remove forgets a script. Conversations already running it are unaffected.
func (e *Engine) Remove(name string) {
	e.mu.Lock()
	delete(e.scripts, name)
	e.mu.Unlock()
}

func (e *Engine) Register(name string, source []byte) (Script, error) {
	if err := e.scan(name, source); err != nil {
		return Script{}, err
	}
	hash := sha256.Sum256(source)
	s := Script{Name: name, SHA256: hex.EncodeToString(hash[:]), source: append([]byte(nil), source...), driverLine: driverLine(source)}
	e.mu.Lock()
	e.scripts[name] = s
	e.mu.Unlock()
	return Script{Name: s.Name, SHA256: s.SHA256}, nil
}

type Choice struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}
type Prompt struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Choices   []Choice  `json:"choices,omitempty"`
	Deadline  time.Time `json:"deadline"`
	AllowBack bool      `json:"allow_back,omitempty"`
	AllowNext bool      `json:"allow_next"`
	// Speaker is the NPC talk flag byte of a say prompt: 1 hides the close
	// button, 2 shows the player as the speaker (Legacy sendNextS type).
	Speaker int `json:"speaker,omitempty"`
	// SpeakerNPC replaces the talking NPC of a scene say prompt.
	SpeakerNPC int `json:"speaker_npc,omitempty"`
	// Wait is the length in milliseconds of a scene wait prompt, which the
	// client answers by itself when the time has passed.
	Wait int `json:"wait,omitempty"`
}
type DebugSnapshot struct {
	Line    int            `json:"line"`
	Locals  map[string]any `json:"locals"`
	Globals map[string]any `json:"globals,omitempty"`
}
type Event struct {
	Kind   string         `json:"kind"`
	Script string         `json:"script"`
	SHA256 string         `json:"sha256"`
	Line   int            `json:"line,omitempty"` // last executed source line
	Prompt *Prompt        `json:"prompt,omitempty"`
	Debug  *DebugSnapshot `json:"debug,omitempty"`
}
type Response struct {
	Identity Identity
	PromptID string
	Kind     string
	Choice   int64
	Bool     bool
	Back     bool
	Finish   bool
	Text     string
}
type StartOptions struct {
	Debug       bool
	Breakpoints []int
	Host        Host
	API         string // "npc" (default), "portal", "map" or "scene", selected by the server host.
}
type Session struct {
	mu                sync.Mutex
	identity          Identity
	script            Script
	limits            Limits
	host              Host
	api               string
	vm                *rt.Runtime
	thread            *rt.Thread
	npc               *rt.Table
	event             Event
	currentContext    context.Context
	stepCPUStart      uint64
	stepDeadline      time.Time
	expires           time.Time
	timer             *time.Timer
	nonce             string
	prompts, commands int
	breakpoints       map[int]bool
	step              bool
	pauseRequested    atomic.Bool
	cancelRequested   atomic.Bool
	closed            bool
	snapshot          DebugSnapshot
	builtinGlobals    map[string]bool
	line              int
	sourceLine        int // s.line, but not inside an appended legacy driver
}

func (e *Engine) Start(ctx context.Context, id Identity, name string, options StartOptions) (s *Session, event Event, err error) {
	if id.SessionID == "" || id.CharacterID <= 0 || id.Generation <= 0 {
		return nil, event, ErrIdentity
	}
	if options.API == "" {
		options.API = "npc"
	}
	if options.API != "npc" && options.API != "portal" && options.API != "map" && options.API != "scene" {
		return nil, event, errors.New("invalid script API context")
	}
	e.mu.RLock()
	script, ok := e.scripts[name]
	e.mu.RUnlock()
	if !ok {
		return nil, event, errors.New("Lua script not registered")
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, event, err
	}
	s = &Session{identity: id, script: script, limits: e.options.Limits, host: options.Host, api: options.API, currentContext: ctx, expires: time.Now().Add(e.options.Limits.SessionTimeout), nonce: hex.EncodeToString(nonce[:]), breakpoints: make(map[int]bool), step: options.Debug}
	if s.host == nil {
		s.host = e.options.Host
	}
	for _, line := range options.Breakpoints {
		if line > 0 {
			s.breakpoints[line] = true
		}
	}
	s.vm = rt.New(io.Discard, rt.WithRuntimeContext(rt.RuntimeContextDef{HardLimits: rt.RuntimeResources{Cpu: s.limits.TotalCPU, Memory: s.limits.MemoryBytes}, RequiredFlags: rt.ComplyCpuSafe | rt.ComplyMemSafe | rt.ComplyTimeSafe | rt.ComplyIoSafe}))
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.vm.MainThread().CallContext(rt.RuntimeContextDef{}, func() error {
		lib.LoadLibs(s.vm, base.LibLoader, packagelib.LibLoader, stringlib.LibLoader, tablelib.LibLoader, mathlib.LibLoader, utf8lib.LibLoader)
		for _, name := range []string{"dofile", "loadfile", "load", "collectgarbage", "print", "warn", "rawset", "setmetatable", "getmetatable", "_G", "package", "require"} {
			s.vm.SetEnv(s.vm.GlobalEnv(), name, rt.NilValue)
		}
		s.installHost()
		chunk, e := compile(s.vm, script.Name, script.source)
		if e != nil {
			return e
		}
		s.thread = rt.NewThread(s.vm)
		s.thread.Start(chunk)
		return nil
	})
	if err != nil {
		s.closeLocked()
		return nil, event, err
	}
	event, err = s.resumeLocked(ctx, nil)
	if err != nil {
		s.closeLocked()
		return nil, event, err
	}
	if !s.closed {
		s.timer = time.AfterFunc(s.limits.SessionTimeout, func() { s.Cancel() })
	}
	return s, event, nil
}

func (s *Session) resumeLocked(ctx context.Context, args []rt.Value) (event Event, err error) {
	if s.closed {
		return event, ErrClosed
	}
	if s.cancelRequested.Load() {
		s.closeLocked()
		return event, ErrCancelled
	}
	if time.Now().After(s.expires) {
		s.closeLocked()
		return event, ErrTimeout
	}
	if err = ctx.Err(); err != nil {
		return event, err
	}
	s.currentContext = ctx
	s.event = Event{}
	s.stepCPUStart = s.vm.UsedResources().Cpu
	s.stepDeadline = time.Now().Add(s.limits.StepTime)
	err = s.resumeThread(args)
	if err != nil {
		s.closeLocked()
		return event, err
	}
	if s.thread.Status() == rt.ThreadDead {
		s.event = Event{Kind: "done", Script: s.script.Name, SHA256: s.script.SHA256}
		s.closeLocked()
	}
	s.event.Line = s.sourceLine
	event = s.event
	if event.Kind == "" {
		s.closeLocked()
		return event, errors.New("script yielded outside host interface")
	}
	return cloneEvent(event), nil
}

func (s *Session) resumeThread(args []rt.Value) (err error) {
	defer func() {
		if value := recover(); value != nil {
			if quota, ok := value.(rt.ContextTerminationError); ok {
				err = quota
			} else {
				panic(value)
			}
		}
	}()
	_, err = s.thread.Resume(s.vm.MainThread(), args)
	return err
}

func (s *Session) Respond(ctx context.Context, response Response) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if response.Identity != s.identity {
		return Event{}, ErrIdentity
	}
	if s.closed {
		return Event{}, ErrClosed
	}
	p := s.event.Prompt
	if s.event.Kind != "prompt" || p == nil || response.PromptID != p.ID || response.Kind != p.Kind {
		return Event{}, ErrStalePrompt
	}
	if time.Now().After(p.Deadline) {
		s.closeLocked()
		return Event{}, ErrTimeout
	}
	var value rt.Value
	switch p.Kind {
	case "say":
		if response.Finish {
			if p.AllowNext || response.Back {
				return Event{}, errors.New("finish is not available at this prompt")
			}
			value = rt.BoolValue(true)
			break
		}
		if response.Back && !p.AllowBack {
			return Event{}, errors.New("back is not allowed at this prompt")
		}
		if !response.Back && !p.AllowNext {
			return Event{}, errors.New("next is not allowed at this prompt")
		}
		value = rt.BoolValue(!response.Back)
	case "yes_no", "accept", "image":
		value = rt.BoolValue(response.Bool)
	case "wait":
		value = rt.BoolValue(true)
	case "menu", "map_selection":
		found := false
		for _, c := range p.Choices {
			if c.ID == response.Choice {
				found = true
			}
		}
		if !found {
			return Event{}, errors.New("choice is not in this prompt")
		}
		value = rt.IntValue(response.Choice)
	case "avatar":
		// The client answers with the index into the offered style list.
		if response.Choice < 0 || response.Choice >= int64(len(p.Choices)) {
			return Event{}, errors.New("choice is not in this prompt")
		}
		value = rt.IntValue(p.Choices[response.Choice].ID)
	case "text":
		if len(response.Text) > 64 {
			return Event{}, errors.New("text answer too long")
		}
		value = rt.StringValue(response.Text)
	default:
		return Event{}, errors.New("unsupported prompt response")
	}
	return s.resumeLocked(ctx, []rt.Value{value})
}
func (s *Session) Continue(ctx context.Context, id Identity) (Event, error) {
	return s.debugResume(ctx, id, false)
}
func (s *Session) Step(ctx context.Context, id Identity) (Event, error) {
	return s.debugResume(ctx, id, true)
}
func (s *Session) debugResume(ctx context.Context, id Identity, step bool) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.identity {
		return Event{}, ErrIdentity
	}
	if s.closed {
		return Event{}, ErrClosed
	}
	if s.event.Kind != "paused" {
		return Event{}, ErrNotPaused
	}
	s.step = step
	return s.resumeLocked(ctx, nil)
}
func (s *Session) RequestPause() { s.pauseRequested.Store(true) }
func (s *Session) SetBreakpoints(id Identity, lines []int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.identity {
		return ErrIdentity
	}
	if s.closed {
		return ErrClosed
	}
	s.breakpoints = make(map[int]bool)
	for _, line := range lines {
		if line > 0 {
			s.breakpoints[line] = true
		}
	}
	return nil
}
func (s *Session) Inspect(id Identity) (DebugSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.identity {
		return DebugSnapshot{}, ErrIdentity
	}
	if s.closed {
		return DebugSnapshot{}, ErrClosed
	}
	if s.event.Kind != "paused" {
		return DebugSnapshot{}, ErrNotPaused
	}
	return cloneSnapshot(s.snapshot), nil
}

func (s *Session) CurrentEvent(id Identity) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.identity {
		return Event{}, ErrIdentity
	}
	if s.closed && s.event.Kind != "done" {
		return Event{}, ErrClosed
	}
	return cloneEvent(s.event), nil
}

// Source returns the source pinned to this invocation, even if its registered
// name has since been reloaded. It never reads an arbitrary filesystem path.
func (s *Session) Source(id Identity) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.identity {
		return "", ErrIdentity
	}
	if s.closed && s.event.Kind != "done" {
		return "", ErrClosed
	}
	return string(s.script.source), nil
}
func (s *Session) Cancel() {
	s.cancelRequested.Store(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}
func (s *Session) closeLocked() {
	if s.closed {
		return
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.vm != nil {
		if s.thread != nil {
			_, _ = s.thread.Close(s.vm.MainThread())
		}
		s.vm.Close(nil)
	}
}

func (s *Session) installHost() {
	s.npc = rt.NewTable()
	s.vm.SetEnv(s.vm.GlobalEnv(), s.api, rt.TableValue(s.npc))
	bind := func(name string, f rt.GoFunctionFunc, nargs int) {
		fn := s.vm.SetEnvGoFunc(s.npc, name, f, nargs, false)
		fn.SolemnlyDeclareCompliance(rt.ComplyCpuSafe | rt.ComplyMemSafe | rt.ComplyTimeSafe | rt.ComplyIoSafe)
	}
	bind("say", s.say, 5)
	bind("ask_yes_no", s.askYesNo, 2)
	bind("ask_accept", s.askAccept, 2)
	bind("menu", s.menu, 3)
	bind("ask_text", s.askText, 2)
	bind("ask_avatar", s.askAvatar, 3)
	bind("ask_map_selection", s.askMapSelection, 2)
	bind("change_look", s.changeLook, 4)
	bind("open_storage", s.simpleCommand("open_storage", 0), 1)
	bind("open_merchant_bank", s.simpleCommand("open_merchant_bank", 0), 1)
	bind("open_parcel", s.simpleCommand("open_parcel", 0), 1)
	bind("open_shop", s.simpleCommand("open_shop", 1), 2)
	bind("warp", s.warpCommand, 3)
	if s.api == "npc" {
		// Beauty salons read the current look to build their style lists.
		bind("gender", s.simpleCommand("gender", 0), 1)
		bind("hair", s.simpleCommand("hair", 0), 1)
		bind("face", s.simpleCommand("face", 0), 1)
		bind("name", s.simpleCommand("name", 0), 1)
		// message(text[, kind]) is Legacy playerMessage/dropMessage: a chat
		// notice line outside the conversation window.
		bind("message", s.messageCommand("message"), 3)
		// Reads and calls the generated Legacy NPC sources need.
		bind("stat", s.textCommand("stat"), 2)
		bind("has_equipped", s.simpleCommand("has_equipped", 1), 2)
		bind("player_count", s.simpleCommand("player_count", 1), 2)
		bind("monster_count", s.simpleCommand("monster_count", 0), 1)
		bind("party", s.simpleCommand("party", 0), 1)
		bind("character_id", s.simpleCommand("character_id", 0), 1)
		bind("open_npc", s.simpleCommand("open_npc", 1), 2)
		bind("map_message", s.messageCommand("map_message"), 3)
		// Event instances (party quests, boss fights): see the README.
		for _, kind := range []string{"event_status", "event_start", "event_get", "event_effect"} {
			bind(kind, s.textCommand(kind), 2)
		}
		bind("event_finish", s.simpleCommand("event_finish", 0), 1)
		bind("event_leave", s.simpleCommand("event_leave", 0), 1)
		bind("event_areas", s.simpleCommand("event_areas", 0), 1)
		bind("event_party_exp", s.simpleCommand("event_party_exp", 1), 2)
		bind("transit_status", s.simpleCommand("transit_status", 0), 1)
		bind("transit_board", s.simpleCommand("transit_board", 0), 1)
		bind("transit_leave", s.simpleCommand("transit_leave", 0), 1)
		bind("subway_challenge", s.simpleCommand("subway_challenge", 1), 2)
		bind("henesys_finish", s.simpleCommand("henesys_finish", 0), 1)
		bind("event_set", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			if err := c.CheckNArgs(3); err != nil {
				return nil, err
			}
			key, err := c.StringArg(1)
			if err != nil {
				return nil, err
			}
			value, err := c.StringArg(2)
			if err != nil || len(key) == 0 || len(key) > 32 || strings.Contains(key, "=") || len(value) > 256 {
				return nil, domain.ErrInvalidInput
			}
			return s.execute(t, c, Command{Kind: "event_set", Text: key + "=" + value})
		}, 3)
		// time() is the server clock a Legacy source read with new Date().
		bind("time", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			now := time.Now()
			v, err := hostValue(t.Runtime, map[string]any{"ms": now.UnixMilli(), "year": int64(now.Year()), "month": int64(now.Month()), "day": int64(now.Day()),
				"hour": int64(now.Hour()), "minute": int64(now.Minute()), "second": int64(now.Second()), "weekday": int64(now.Weekday())}, 0)
			if err != nil {
				return nil, err
			}
			return c.PushingNext1(t.Runtime, v), nil
		}, 1)
	}
	if s.api == "scene" {
		s.installScene(bind)
	}
	if s.api == "map" {
		bind("gender", s.simpleCommand("gender", 0), 1)
		bind("play_scene", func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
			if err := c.CheckNArgs(2); err != nil {
				return nil, err
			}
			path, err := c.StringArg(1)
			if err != nil {
				return nil, err
			}
			if len(path) == 0 || len(path) > 160 || !strings.HasPrefix(path, "Effect/Direction") || strings.Contains(path, "..") || strings.ContainsAny(path, "\\\x00") {
				return nil, domain.ErrInvalidInput
			}
			return s.execute(t, c, Command{Kind: "play_scene", Text: path})
		}, 2)
	}
	if s.api == "portal" {
		// message(text[, kind]) is Legacy playerMessage: kind 5 (the default)
		// or 6 is a chat notice line; the host maps other kinds to 5.
		bind("message", s.messageCommand("portal_message"), 3)
		bind("portal_name", s.simpleCommand("portal_name", 0), 1)
		bind("portal_id", s.simpleCommand("portal_id", 0), 1)
		bind("elevator_board", s.simpleCommand("elevator_board", 0), 1)
		bind("player_count", s.simpleCommand("player_count", 1), 2)
		bind("monster_count", s.simpleCommand("monster_count", 0), 1)
		bind("open_npc", s.simpleCommand("open_npc", 1), 2)
		// Legacy getFame/isGM and the party reads behind getParty/isLeader.
		bind("fame", s.simpleCommand("fame", 0), 1)
		bind("gm", s.simpleCommand("gm", 0), 1)
		bind("party_size", s.simpleCommand("party_size", 0), 1)
		bind("party_leader", s.simpleCommand("party_leader", 0), 1)
		// warp_party(map, portal id) is Legacy warpParty: the character and
		// every party member standing on the same map move together.
		bind("warp_party", s.simpleCommand("warp_party", 2), 3)
		bind("gender", s.simpleCommand("gender", 0), 1)
	}
	bind("quest_state", s.simpleCommand("quest_state", 1), 2)
	bind("quest_data", s.simpleCommand("quest_data", 1), 2)
	bind("quest_start", s.questCommand("quest_start"), 2)
	bind("quest_complete", s.questCommand("quest_complete"), 2)
	bind("quest_forfeit", s.questCommand("quest_forfeit"), 2)
	bind("hp", s.simpleCommand("hp", 0), 1)
	bind("map_id", s.simpleCommand("map_id", 0), 1)
	bind("level", s.simpleCommand("level", 0), 1)
	bind("job", s.simpleCommand("job", 0), 1)
	bind("item_count", s.simpleCommand("item_count", 1), 2)
	bind("can_hold", s.simpleCommand("can_hold", 2), 3)
	bind("mesos", s.simpleCommand("mesos", 0), 1)
	bind("depart_island", s.simpleCommand("depart_island", 0), 1)
	bind("take_taxi", s.simpleCommand("take_taxi", 2), 3)
	bind("advance_warrior", s.simpleCommand("advance_warrior", 0), 1)
	bind("advance_first_job", s.simpleCommand("advance_first_job", 1), 2)
	bind("advance_second_job", s.simpleCommand("advance_second_job", 1), 2)
	// Professions (Skill.wz 9200-9204). The host applies the learning rules
	// and grants the starter tool; a script only names the profession.
	bind("profession_level", s.simpleCommand("profession_level", 1), 2)
	bind("profession_learn", s.simpleCommand("profession_learn", 1), 2)
	bind("profession_forget", s.simpleCommand("profession_forget", 1), 2)
	bind("open_crafting", s.simpleCommand("open_crafting", 0), 1)
	bind("exchange", s.exchange, 2)
	// Guild office (2010007/2010008) and alliance (2010009) services.
	bind("guild_rank", s.simpleCommand("guild_rank", 0), 1)
	bind("guild_prompt", s.simpleCommand("guild_prompt", 1), 2)
	bind("guild_disband", s.simpleCommand("guild_disband", 0), 1)
	bind("guild_capacity", s.simpleCommand("guild_capacity", 1), 2)
	bind("guild_capacity_limit", s.simpleCommand("guild_capacity_limit", 1), 2)
	bind("alliance_rank", s.simpleCommand("alliance_rank", 0), 1)
	bind("alliance_create", s.textCommand("alliance_create"), 2)
	bind("alliance_capacity", s.simpleCommand("alliance_capacity", 0), 1)
	bind("alliance_disband", s.simpleCommand("alliance_disband", 0), 1)
	bind("log", s.readLog, 4)
	fn := s.vm.SetEnvGoFunc(s.vm.GlobalEnv(), "__maple_trace", s.trace, 2, false)
	fn.SolemnlyDeclareCompliance(rt.ComplyCpuSafe | rt.ComplyMemSafe | rt.ComplyTimeSafe | rt.ComplyIoSafe)
	s.builtinGlobals = make(map[string]bool)
	key := rt.NilValue
	for scanned := 0; scanned < 256; scanned++ {
		next, _, ok := s.vm.GlobalEnv().Next(key)
		if !ok || next.IsNil() {
			break
		}
		key = next
		if name, ok := next.TryString(); ok {
			s.builtinGlobals[name] = true
		}
	}
}

func (s *Session) prompt(t *rt.Thread, c *rt.GoCont, kind, text string, choices []Choice) (rt.Cont, error) {
	limit := 64
	if kind == "avatar" {
		limit = 255 // the native style list carries a U8 count
	}
	if len(text) > 8192 || len(choices) > limit {
		return nil, errors.New("prompt exceeds content limit")
	}
	if s.prompts >= s.limits.MaxPrompts {
		return nil, errors.New("prompt count limit exceeded")
	}
	s.prompts++
	s.event = Event{Kind: "prompt", Script: s.script.Name, SHA256: s.script.SHA256, Prompt: &Prompt{ID: fmt.Sprintf("%s-%d", s.nonce, s.prompts), Kind: kind, Text: text, Choices: choices, Deadline: time.Now().Add(s.limits.PromptTimeout)}}
	values, err := t.Yield(nil)
	if err != nil {
		return nil, err
	}
	if s.cancelRequested.Load() {
		t.TerminateContext("script session cancelled")
	}
	return c.PushingNext(t.Runtime, values...), nil
}
func (s *Session) say(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	text, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	back := false
	if c.NArgs() > 2 && !c.Arg(2).IsNil() {
		back, err = c.BoolArg(2)
		if err != nil {
			return nil, err
		}
	}
	next := true
	if c.NArgs() > 3 && !c.Arg(3).IsNil() {
		next, err = c.BoolArg(3)
		if err != nil {
			return nil, err
		}
	}
	speaker := int64(0)
	if c.NArgs() > 4 && !c.Arg(4).IsNil() {
		speaker, err = c.IntArg(4)
		if err != nil {
			return nil, err
		}
		if speaker < 0 || speaker > 3 {
			return nil, errors.New("say speaker must be 0-3")
		}
	}
	return s.promptWithBack(t, c, text, back, next, int(speaker))
}

func (s *Session) promptWithBack(t *rt.Thread, c *rt.GoCont, text string, back, next bool, speaker int) (rt.Cont, error) {
	if len(text) > 8192 || s.prompts >= s.limits.MaxPrompts {
		return nil, errors.New("prompt limit exceeded")
	}
	s.prompts++
	s.event = Event{Kind: "prompt", Script: s.script.Name, SHA256: s.script.SHA256, Prompt: &Prompt{ID: fmt.Sprintf("%s-%d", s.nonce, s.prompts), Kind: "say", Text: text, AllowBack: back, AllowNext: next, Speaker: speaker, Deadline: time.Now().Add(s.limits.PromptTimeout)}}
	values, err := t.Yield(nil)
	if err != nil {
		return nil, err
	}
	if s.cancelRequested.Load() {
		return nil, ErrCancelled
	}
	return c.PushingNext(t.Runtime, values...), nil
}
func (s *Session) askYesNo(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	text, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	return s.prompt(t, c, "yes_no", text, nil)
}

// askAccept is Legacy askAcceptDecline: a yes/no answer with quest buttons.
func (s *Session) askAccept(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	text, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	return s.prompt(t, c, "accept", text, nil)
}

// askText is Legacy cm.sendGetText; the answer is a bounded string.
func (s *Session) askText(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	text, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	return s.prompt(t, c, "text", text, nil)
}

func (s *Session) menu(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(3); err != nil {
		return nil, err
	}
	text, err := c.StringArg(1)
	if err != nil {
		return nil, err
	}
	table, err := c.TableArg(2)
	if err != nil {
		return nil, err
	}
	choices, err := menuChoices(table)
	if err != nil {
		return nil, err
	}
	return s.prompt(t, c, "menu", text, choices)
}

// askMapSelection is Legacy cm.askMapSelection: the client's slide menu
// (次元之鏡) lists {id=, text=} destinations and the call returns the
// chosen id. Labels cannot contain '#', which delimits the native list.
func (s *Session) askMapSelection(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	table, err := c.TableArg(1)
	if err != nil {
		return nil, err
	}
	choices, err := menuChoices(table)
	if err != nil {
		return nil, err
	}
	for _, choice := range choices {
		if choice.Text == "" || len(choice.Text) > 200 || strings.ContainsRune(choice.Text, '#') || choice.ID < 0 || choice.ID > 2147483647 {
			return nil, errors.New("invalid map selection entry")
		}
	}
	return s.prompt(t, c, "map_selection", "", choices)
}

func menuChoices(table *rt.Table) ([]Choice, error) {
	if table.Len() < 1 || table.Len() > 64 {
		return nil, errors.New("menu requires 1..64 choices")
	}
	choices := make([]Choice, 0, table.Len())
	seen := map[int64]bool{}
	for i := int64(1); i <= table.Len(); i++ {
		v := table.Get(rt.IntValue(i))
		item, ok := v.TryTable()
		if !ok {
			return nil, errors.New("menu choice must be a table")
		}
		id, ok := item.Get(rt.StringValue("id")).TryInt()
		if !ok || seen[id] {
			return nil, errors.New("invalid or duplicate menu choice id")
		}
		label, ok := item.Get(rt.StringValue("text")).TryString()
		if !ok || len(label) > 1024 {
			return nil, errors.New("invalid menu choice text")
		}
		seen[id] = true
		choices = append(choices, Choice{ID: id, Text: label})
	}
	return choices, nil
}

func (s *Session) trace(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if s.cancelRequested.Load() {
		t.TerminateContext("script session cancelled")
	}
	if err := s.currentContext.Err(); err != nil {
		t.TerminateContext("script context cancelled: %v", err)
	}
	if t.UsedResources().Cpu-s.stepCPUStart > s.limits.StepCPU || time.Now().After(s.stepDeadline) {
		t.TerminateContext("Lua step execution budget exceeded")
	}
	line, err := c.IntArg(0)
	if err != nil {
		return nil, err
	}
	s.line = int(line)
	if s.script.driverLine == 0 || s.line < s.script.driverLine {
		s.sourceLine = s.line
	}
	pause := s.pauseRequested.Swap(false)
	if !s.step && !pause && !s.breakpoints[int(line)] {
		return c.Next(), nil
	}
	table, err := c.TableArg(1)
	if err != nil {
		return nil, err
	}
	locals := map[string]any{}
	key := rt.NilValue
	for count := 0; count < 128; count++ {
		k, v, ok := table.Next(key)
		if !ok || k.IsNil() {
			break
		}
		key = k
		name, ok := k.TryString()
		if !ok {
			continue
		}
		wrapper, ok := v.TryTable()
		if !ok {
			continue
		}
		locals[name] = snapshotValue(wrapper.Get(rt.StringValue("value")), 0)
	}
	globals := make(map[string]any)
	key = rt.NilValue
	for scanned := 0; scanned < 512; scanned++ {
		k, v, ok := s.vm.GlobalEnv().Next(key)
		if !ok || k.IsNil() {
			break
		}
		key = k
		if name, ok := k.TryString(); ok && !s.builtinGlobals[name] {
			globals[name] = snapshotValue(v, 0)
			if len(globals) >= 128 {
				break
			}
		}
	}
	s.snapshot = DebugSnapshot{Line: int(line), Locals: locals, Globals: globals}
	s.event = Event{Kind: "paused", Script: s.script.Name, SHA256: s.script.SHA256, Debug: &s.snapshot}
	s.step = false
	_, err = t.Yield(nil)
	return c.Next(), err
}

func (s *Session) execute(t *rt.Thread, c *rt.GoCont, command Command) (rt.Cont, error) {
	result, err := s.runHost(command)
	if err != nil {
		return nil, err
	}
	v, err := hostValue(t.Runtime, result.Value, 0)
	if err != nil {
		return nil, err
	}
	return c.PushingNext1(t.Runtime, v), nil
}

// runHost sends one bounded command to the host with a fresh operation ID.
func (s *Session) runHost(command Command) (CommandResult, error) {
	if s.host == nil {
		return CommandResult{}, errors.New("host capability is unavailable")
	}
	if s.commands >= s.limits.MaxCommands {
		return CommandResult{}, errors.New("host command limit exceeded")
	}
	if s.cancelRequested.Load() {
		return CommandResult{}, ErrCancelled
	}
	s.commands++
	command.ID = fmt.Sprintf("lua-%s-%s-%d", s.nonce, s.script.SHA256[:16], s.commands)
	command.Script = s.script.Name
	command.Line = s.sourceLine
	ctx, cancel := context.WithTimeout(s.currentContext, 2*time.Second)
	defer cancel()
	return s.host.Execute(ctx, s.identity, command)
}

// messageCommand sends one chat notice line with an optional Legacy kind.
func (s *Session) messageCommand(command string) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(2); err != nil {
			return nil, err
		}
		text, err := c.StringArg(1)
		if err != nil {
			return nil, err
		}
		if len(text) == 0 || len(text) > 512 || strings.ContainsRune(text, 0) {
			return nil, domain.ErrInvalidInput
		}
		command := Command{Kind: command, Text: text}
		if c.NArgs() > 2 && !c.Arg(2).IsNil() {
			kind, err := c.IntArg(2)
			if err != nil || kind < -8 || kind > 25 {
				return nil, domain.ErrInvalidInput
			}
			command.Ints = []int64{kind}
		}
		return s.execute(t, c, command)
	}
}

// textCommand passes one bounded string argument to the host.
func (s *Session) textCommand(kind string) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(2); err != nil {
			return nil, err
		}
		text, err := c.StringArg(1)
		if err != nil || text == "" || len(text) > 64 || strings.ContainsRune(text, '\x00') {
			return nil, errors.New("invalid text argument")
		}
		return s.execute(t, c, Command{Kind: kind, Text: text})
	}
}

func (s *Session) simpleCommand(kind string, count int) rt.GoFunctionFunc {
	return func(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
		if err := c.CheckNArgs(count + 1); err != nil {
			return nil, err
		}
		args := make([]int64, count)
		for i := range args {
			v, e := c.IntArg(i + 1)
			if e != nil {
				return nil, e
			}
			args[i] = v
		}
		return s.execute(t, c, Command{Kind: kind, Ints: args})
	}
}

func snapshotValue(v rt.Value, depth int) any {
	if v.IsNil() {
		return nil
	}
	if x, ok := v.TryString(); ok {
		if len(x) > 2048 {
			return x[:2048] + "…"
		}
		return x
	}
	if x, ok := v.TryInt(); ok {
		return x
	}
	if x, ok := v.TryFloat(); ok {
		return x
	}
	if x, ok := v.TryBool(); ok {
		return x
	}
	if table, ok := v.TryTable(); ok {
		if depth >= 2 {
			return "<table>"
		}
		out := map[string]any{}
		key := rt.NilValue
		for i := 0; i < 32; i++ {
			k, val, ok := table.Next(key)
			if !ok || k.IsNil() {
				break
			}
			key = k
			ks, ok := k.ToString()
			if ok {
				out[ks] = snapshotValue(val, depth+1)
			}
		}
		return out
	}
	return fmt.Sprintf("<type:%d>", v.Type())
}
func hostValue(r *rt.Runtime, v any, depth int) (rt.Value, error) {
	if depth > 3 {
		return rt.NilValue, errors.New("host result nesting limit")
	}
	switch x := v.(type) {
	case nil:
		return rt.NilValue, nil
	case bool:
		return rt.BoolValue(x), nil
	case string:
		if len(x) > 8192 {
			return rt.NilValue, errors.New("host string limit")
		}
		r.RequireBytes(len(x))
		return rt.StringValue(x), nil
	case int:
		return rt.IntValue(int64(x)), nil
	case int64:
		return rt.IntValue(x), nil
	case map[string]any:
		if len(x) > 64 {
			return rt.NilValue, errors.New("host map limit")
		}
		table := rt.NewTable()
		r.RequireBytes(256)
		for k, item := range x {
			value, err := hostValue(r, item, depth+1)
			if err != nil {
				return rt.NilValue, err
			}
			r.SetEnv(table, k, value)
		}
		return rt.TableValue(table), nil
	default:
		return rt.NilValue, fmt.Errorf("unsupported host value %T", v)
	}
}
func cloneSnapshot(s DebugSnapshot) DebugSnapshot {
	out := DebugSnapshot{Line: s.Line, Locals: map[string]any{}, Globals: map[string]any{}}
	for k, v := range s.Locals {
		out.Locals[k] = clonePlain(v)
	}
	for k, v := range s.Globals {
		out.Globals[k] = clonePlain(v)
	}
	return out
}
func clonePlain(v any) any {
	if m, ok := v.(map[string]any); ok {
		n := make(map[string]any, len(m))
		for k, x := range m {
			n[k] = clonePlain(x)
		}
		return n
	}
	return v
}
func cloneEvent(e Event) Event {
	if e.Prompt != nil {
		p := *e.Prompt
		p.Choices = append([]Choice(nil), p.Choices...)
		e.Prompt = &p
	}
	if e.Debug != nil {
		d := cloneSnapshot(*e.Debug)
		e.Debug = &d
	}
	return e
}
