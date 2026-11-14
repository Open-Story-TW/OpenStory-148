package world

import (
	"context"
	"strings"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/scripts"
)

// TestGMSeesPortalScriptTrace steps on script portals as a player and as a
// GM: a generated Legacy source that warps, a Legacy script still pending
// conversion, and a script name neither server has.
func TestGMSeesPortalScriptTrace(t *testing.T) {
	_, pending, err := scripts.LegacyPortalMigrations()
	if err != nil || len(pending) == 0 {
		t.Fatal("no pending Legacy portal scripts", err)
	}
	pendingName := strings.TrimSuffix(strings.TrimPrefix(pending[0].Legacy, "portal/"), ".js")
	for _, gm := range []int{0, 1} {
		sw, err := newScriptWorld()
		if err != nil {
			t.Fatal(err)
		}
		// A generated source keeps its embedded name, so the host treats it
		// as legacy; its body is swapped for a destination the test catalog has.
		driver, _ := scripts.Source("portal/go_FantasticPark.lua")
		body := scripts.LegacyPortalMarker + "\nfunction enter(pi)\n    pi.playerMessage(5, \"welcome\")\n    pi.warp(20000, 0)\nend\n" +
			string(driver[strings.Index(string(driver), "\n-- Shared driver")+1:])
		if _, err = sw.engine.Register("portal/go_FantasticPark.lua", []byte(body)); err != nil {
			t.Fatal(err)
		}
		gameplay, err := newGameplaySpace()
		if err != nil {
			t.Fatal(err)
		}
		var saved []domain.Character
		s := &Server{scripts: sw, gameplay: gameplay, maps: newMapSpace(800), drops: newDropSpace(), online: make(map[int64]domain.CharacterSession), repo: gameplayTestRepo{save: func(_ domain.CharacterSession, c domain.Character) error {
			saved = append(saved, c)
			return nil
		}}}
		live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, SessionID: "gm-portal-trace", Generation: 1, ChannelID: 1, Character: domain.Character{ID: 1, GM: gm, MapID: 10000, HP: 50, X: 1077, Y: 480, Revision: 1}}}
		sink := &memSink{}
		seedBoundScene(t, s, live, sink)
		for _, script := range []string{"go_FantasticPark", pendingName, "no_such_portal_script"} {
			live.session.Character.MapID, live.session.Character.X, live.session.Character.Y = 10000, 1077, 480
			p := content.Portal{Name: "in01", Script: script, X: 1077, Y: 480}
			if err = s.runPortalScript(context.Background(), 1, live, sink, p); err != nil {
				t.Fatalf("%s: %v", script, err)
			}
		}
		if len(saved) != 1 || saved[0].MapID != 20000 {
			t.Fatalf("legacy portal did not warp: %+v", saved)
		}
		var notices []string
		for _, text := range noticeTexts(sink) {
			if strings.HasPrefix(text, "[GM") {
				notices = append(notices, text)
			}
		}
		if gm == 0 {
			if len(notices) != 0 {
				t.Fatalf("player received GM notices %q", notices)
			}
			continue
		}
		want := []string{
			"[GM Portal] 10000/in01「go_FantasticPark」 portal/go_FantasticPark.lua 開始",
			"[GM Portal] 10000/in01「go_FantasticPark」 portal/go_FantasticPark.lua 第3行 指令 portal_message(5) → 完成",
			"[GM Portal] 10000/in01「go_FantasticPark」 portal/go_FantasticPark.lua 第",
			"[GM Portal] 10000/in01「go_FantasticPark」 portal/go_FantasticPark.lua 第",
			"[GM Portal] 10000/in01「" + pendingName + "」 未處理(Legacy portal/" + pendingName + ".js 尚未移植: " + pending[0].Reason + ")",
			"[GM Portal] 10000/in01「no_such_portal_script」 未處理(伺服器與 Legacy 都沒有這個傳點腳本)",
		}
		if len(notices) != len(want) {
			t.Fatalf("notices %q", notices)
		}
		for i := range want {
			if !strings.HasPrefix(notices[i], want[i]) {
				t.Fatalf("notice %d = %q, want %q", i, notices[i], want[i])
			}
		}
		if !strings.Contains(notices[2], "指令 warp(20000,0) → 完成") || !strings.HasSuffix(notices[3], "結束") {
			t.Fatalf("warp/end notices %q", notices[2:4])
		}
	}
	_ = protocol.EnableActions
}
