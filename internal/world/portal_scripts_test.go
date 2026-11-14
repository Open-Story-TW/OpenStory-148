package world

import (
	"context"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

func TestPortalLuaDebuggerAndMovementInvalidation(t *testing.T) {
	for _, leave := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume warp", true: "left portal while paused"}[leave], func(t *testing.T) {
			scripts, err := newScriptWorld()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = scripts.engine.Register("portal/bigban.lua", []byte("local destination = 20000\nportal:warp(destination, 0)")); err != nil {
				t.Fatal(err)
			}
			gameplay, err := newGameplaySpace()
			if err != nil {
				t.Fatal(err)
			}
			var saved []domain.Character
			s := &Server{scripts: scripts, gameplay: gameplay, maps: newMapSpace(800), drops: newDropSpace(), online: make(map[int64]domain.CharacterSession), repo: gameplayTestRepo{save: func(_ domain.CharacterSession, c domain.Character) error {
				saved = append(saved, c)
				return nil
			}}}
			live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, SessionID: "portal-test-session", Generation: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 10000, HP: 50, X: 1077, Y: 480, Revision: 1}}}
			identity := scripting.Identity{SessionID: live.session.SessionID, CharacterID: 1, Generation: 1}
			scripts.armed[1] = scriptDebugArm{identity: identity, options: scripting.StartOptions{Debug: true}}
			sink := &gameplayTestSink{}
			seedBoundScene(t, s, live, sink)
			p := content.Portal{Script: "bigban", X: 1077, Y: 480}
			if err = s.runPortalScript(context.Background(), 1, live, sink, p); err != nil {
				t.Fatal(err)
			}
			active := scripts.active[1]
			if active == nil || active.kind != "portal" || len(saved) != 0 {
				t.Fatal("portal did not suspend before its effect")
			}
			event, err := active.session.Step(context.Background(), identity)
			if err != nil || event.Kind != "paused" || event.Debug.Line != 2 || event.Debug.Locals["destination"] != int64(20000) {
				t.Fatalf("portal step: %+v %v", event, err)
			}
			if leave {
				live.session.Character.X = 0
			}
			event, err = active.session.Continue(context.Background(), identity)
			if leave {
				if err == nil || len(saved) != 0 {
					t.Fatal("paused script warped a player who left its portal")
				}
				_ = active.fail(err)
			} else {
				if err != nil || event.Kind != "done" || len(saved) != 1 || saved[0].MapID != 20000 {
					t.Fatalf("portal failed to commit destination: %+v %v", saved, err)
				}
				if err = active.deliver(event); err != nil || sink.count(protocol.SendSetField) != 1 {
					t.Fatal("portal did not deliver its map change")
				}
			}
			if scripts.active[1] != nil {
				t.Fatal("finished portal left an active invocation")
			}
		})
	}
}
