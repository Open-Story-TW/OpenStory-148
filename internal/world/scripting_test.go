package world

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

func TestLuaNPCWorldAdapterAndAuthenticatedDebugger(t *testing.T) {
	scripts, err := newScriptWorld()
	if err != nil {
		t.Fatal(err)
	}
	_, err = scripts.engine.Register("npc/2101.lua", []byte("local count = 41\ncount = count + 1\nnpc:say(tostring(count))"))
	if err != nil {
		t.Fatal(err)
	}
	gameplay, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	room, err := gameplay.room(mapKey{channel: 1, mapID: 10000})
	if err != nil {
		t.Fatal(err)
	}
	var npcID int32
	for id, npc := range room.npcs {
		if npc.TemplateID == 2101 {
			npcID = id
		}
	}
	if npcID == 0 {
		t.Fatal("missing source NPC2101")
	}
	first := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, SessionID: "lua-world-first", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 10000, X: 130, Y: 305}}}
	second := &liveCharacter{session: domain.CharacterSession{CharacterID: 2, SessionID: "lua-world-second", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 10000, X: 130, Y: 305}}}
	s := &Server{cfg: platform.Config{RPCToken: "test-debug-secret"}, scripts: scripts, gameplay: gameplay, online: map[int64]domain.CharacterSession{1: first.session, 2: second.session}}
	call := func(auth string, body scriptDebugRequest) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		request := httptest.NewRequest("POST", "/debug/scripts", bytes.NewReader(raw))
		request.Header.Set("Authorization", auth)
		response := httptest.NewRecorder()
		s.scriptDebugger(response, request)
		return response
	}
	if r := call("", scriptDebugRequest{Action: "list"}); r.Code != 401 {
		t.Fatalf("unauthenticated debugger=%d", r.Code)
	}
	arm := scriptDebugRequest{Action: "arm", CharacterID: 1, SessionID: first.session.SessionID, Generation: 3}
	if r := call("Bearer test-debug-secret", arm); r.Code != 200 {
		t.Fatalf("arm=%s", r.Body.String())
	}
	firstSink, secondSink := &memSink{}, &memSink{}
	if handled, e := s.startNPCScript(context.Background(), 1, first, firstSink, npcID); !handled || e != nil {
		t.Fatalf("start=%v %v", handled, e)
	}
	a := scripts.active[1]
	event, e := a.session.CurrentEvent(a.identity)
	if e != nil || event.Kind != "paused" || event.Debug.Line != 1 {
		t.Fatalf("initial pause=%+v %v", event, e)
	}
	// A second character's script must still run through its own prompt.
	if handled, e := s.startNPCScript(context.Background(), 1, second, secondSink, npcID); !handled || e != nil {
		t.Fatalf("peer start=%v %v", handled, e)
	}
	if len(secondSink.pkts) != 1 || secondSink.opcodes()[0] != protocol.SendNPCTalk {
		t.Fatalf("other player's dialogue blocked: %+v", secondSink.opcodes())
	}
	debug := scriptDebugRequest{Action: "step", CharacterID: 1, SessionID: first.session.SessionID, Generation: 3, SHA256: event.SHA256}
	for wantLine := 2; wantLine <= 3; wantLine++ {
		r := call("Bearer test-debug-secret", debug)
		if r.Code != 200 {
			t.Fatalf("step=%s", r.Body.String())
		}
		if e = json.Unmarshal(r.Body.Bytes(), &event); e != nil {
			t.Fatal(e)
		}
		if event.Debug == nil || event.Debug.Line != wantLine {
			t.Fatalf("step line=%+v", event)
		}
		want := int64(41)
		if wantLine == 3 {
			want = 42
		}
		if int64(event.Debug.Locals["count"].(float64)) != want {
			t.Fatalf("locals=%+v", event.Debug.Locals)
		}
	}
	debug.Action = "inspect"
	if r := call("Bearer test-debug-secret", debug); r.Code != 200 {
		t.Fatalf("inspect=%s", r.Body.String())
	}
	debug.Action = "source"
	if r := call("Bearer test-debug-secret", debug); r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte("local count = 41")) {
		t.Fatalf("pinned source=%s", r.Body.String())
	}
	debug.SHA256 = "stale"
	if r := call("Bearer test-debug-secret", debug); r.Code != 409 {
		t.Fatal("stale script hash accepted")
	}
	debug.SHA256 = event.SHA256
	debug.Action = "continue"
	if r := call("Bearer test-debug-secret", debug); r.Code != 200 {
		t.Fatalf("continue=%s", r.Body.String())
	}
	if len(firstSink.pkts) != 1 || firstSink.opcodes()[0] != protocol.SendNPCTalk {
		t.Fatalf("debug resume did not deliver native dialogue: %+v", firstSink.opcodes())
	}
	if handled, e := s.respondNPCScript(context.Background(), first, protocol.NewReader([]byte{0, 1})); !handled || e != nil {
		t.Fatalf("native response=%v %v", handled, e)
	}
	if scripts.active[1] != nil {
		t.Fatal("completed script retained")
	}
	s.cancelNPCScript(second)
	if scripts.active[2] != nil {
		t.Fatal("cancel retained peer script")
	}
	// An arm captured before a reconnect may not pause the new login. This
	// remains true even if disconnect cleanup raced the debugger request.
	scripts.armed[1] = scriptDebugArm{identity: a.identity, options: scripting.StartOptions{Debug: true}}
	first.session.Generation++
	s.online[1] = first.session
	if handled, e := s.startNPCScript(context.Background(), 1, first, firstSink, npcID); !handled || e != nil {
		t.Fatalf("reconnect start=%v %v", handled, e)
	}
	fresh := scripts.active[1]
	if event, e := fresh.session.CurrentEvent(fresh.identity); e != nil || event.Kind != "prompt" {
		t.Fatalf("stale debugger arm crossed login generation: %+v %v", event, e)
	}
	s.cancelNPCScript(first)
}

func TestApplyScriptOrStorageInventoryIncludesMesos(t *testing.T) {
	s := &Server{online: make(map[int64]domain.CharacterSession)}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, Mesos: 1000, X: 5, Y: 6}}}
	s.applyInventoryResult(live, domain.Character{ID: 1, Mesos: 900, Revision: 2})
	if live.session.Character.Mesos != 900 || live.session.Character.X != 5 || live.session.Character.Y != 6 {
		t.Fatalf("incorrect live exchange merge: %+v", live.session.Character)
	}
}

func TestLuaNPCRegistrationPrecedesNativeSpawns(t *testing.T) {
	scripts, err := newScriptWorld()
	if err != nil {
		t.Fatal(err)
	}
	gameplay, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{scripts: scripts, gameplay: gameplay}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, MapID: 10000}}}
	sink := &memSink{}
	if err = s.enterGameplay(1, live, sink); err != nil {
		t.Fatal(err)
	}
	ops := sink.opcodes()
	if len(ops) < 2 || ops[0] != 0x1db || ops[1] != protocol.SendSpawnNPC {
		t.Fatalf("NPC source must be registered before construction: %+v", ops)
	}
	r := protocol.NewReader(sink.pkts[0])
	r.U16()
	if r.U8() != 2 {
		t.Fatal("only the two visible reviewed starter NPCs should be registered")
	}
}

func TestInventoryReplayDoesNotHealUncheckpointedDamage(t *testing.T) {
	s := &Server{online: make(map[int64]domain.CharacterSession)}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, HP: 12, MP: 2, MaxHP: 50, MaxMP: 5, Revision: 4}}}
	s.applyInventoryResult(live, domain.Character{ID: 1, HP: 50, MP: 5, MaxHP: 50, MaxMP: 5, Revision: 4})
	if live.session.Character.HP != 12 || live.session.Character.MP != 2 {
		t.Fatal("replayed asset command restored old checkpoint vitals")
	}
}
