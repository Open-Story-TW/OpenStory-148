package world

import (
	"context"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

func TestLuaNativeBoolNavigationAndCancellation(t *testing.T) {
	for _, prompt := range []struct {
		name string
		wire byte
		call string
	}{{"say", 0, `npc:say("Both buttons", true, true)`}, {"yes_no", 2, `npc:ask_yes_no("Yes or no")`}} {
		for _, mode := range []byte{0, 1, 255} {
			t.Run(prompt.name+"/"+map[byte]string{0: "false", 1: "true", 255: "cancel"}[mode], func(t *testing.T) {
				scripts, err := newScriptWorld()
				if err != nil {
					t.Fatal(err)
				}
				_, err = scripts.engine.Register("npc/navigation-test.lua", []byte("local forward = "+prompt.call+`
if forward then npc:say("true branch",false,false)
else npc:say("false branch",false,false) end
`))
				if err != nil {
					t.Fatal(err)
				}
				identity := scripting.Identity{SessionID: "navigation-native-test", CharacterID: 101, Generation: 1}
				live := &liveCharacter{session: domain.CharacterSession{SessionID: identity.SessionID, CharacterID: identity.CharacterID, Generation: identity.Generation, Character: domain.Character{ID: 101}}}
				sink := &memSink{}
				s := &Server{scripts: scripts}
				active := &worldScript{server: s, live: live, sink: sink, identity: identity, npcID: 2100}
				session, event, err := scripts.engine.Start(context.Background(), identity, "npc/navigation-test.lua", scripting.StartOptions{Host: active})
				if err != nil {
					t.Fatal(err)
				}
				defer session.Cancel()
				active.session = session
				scripts.active[101] = active
				if event.Prompt.Kind != prompt.name || prompt.name == "say" && (!event.Prompt.AllowBack || !event.Prompt.AllowNext) {
					t.Fatal("missing navigation options")
				}
				if err = active.deliver(event); err != nil {
					t.Fatal(err)
				}
				handled, err := s.respondNPCScript(context.Background(), live, protocol.NewReader([]byte{prompt.wire, mode}))
				if !handled || err != nil {
					t.Fatalf("response: %v %v", handled, err)
				}
				if mode == 255 {
					if scripts.active[101] != nil {
						t.Fatal("cancel resumed or retained script")
					}
					if len(sink.pkts) != 2 || sink.opcodes()[1] != protocol.SendUpdateStats {
						t.Fatalf("cancel emitted another dialogue: %v", sink.opcodes())
					}
					return
				}
				next, err := session.CurrentEvent(identity)
				want := map[byte]string{0: "false branch", 1: "true branch"}[mode]
				if err != nil || next.Kind != "prompt" || next.Prompt.Text != want {
					t.Fatalf("branch: %+v %v", next, err)
				}
			})
		}
	}
}
