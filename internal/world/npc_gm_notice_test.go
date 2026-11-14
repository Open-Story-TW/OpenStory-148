package world

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

func noticeTexts(sink *memSink) []string {
	var out []string
	for _, p := range sink.pkts {
		r := protocol.NewReader(p)
		if r.U16() == 0x58 && r.U8() == 5 {
			out = append(out, r.String())
		}
	}
	return out
}

func TestGMSeesNPCConversationTrace(t *testing.T) {
	for _, gm := range []int{0, 1} {
		sw, err := newScriptWorld()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = sw.engine.Register("npc/2101.lua", []byte("local c = npc:menu(\"Pick\", {{id=1,text=\"A\"},{id=4,text=\"B\"}})\nnpc:say(\"bye\", false, false)")); err != nil {
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
		var oid int32
		for id, npc := range room.npcs {
			if npc.TemplateID == 2101 {
				oid = id
			}
		}
		live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, SessionID: "gm-npc-trace", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 1, GM: gm, MapID: 10000, X: 130, Y: 305}}}
		s := &Server{scripts: sw, gameplay: gameplay}
		sink := &memSink{}
		if handled, e := s.startNPCScript(context.Background(), 1, live, sink, oid); !handled || e != nil {
			t.Fatalf("start=%v %v", handled, e)
		}
		if handled, e := s.respondNPCScript(context.Background(), live, protocol.NewReader([]byte{5, 1, 4, 0, 0, 0})); !handled || e != nil {
			t.Fatalf("menu=%v %v", handled, e)
		}
		if handled, e := s.respondNPCScript(context.Background(), live, protocol.NewReader([]byte{0, 1})); !handled || e != nil {
			t.Fatalf("finish=%v %v", handled, e)
		}
		got := noticeTexts(sink)
		if gm == 0 {
			if len(got) != 0 {
				t.Fatalf("player received GM notices %q", got)
			}
			continue
		}
		want := []string{
			"[GM NPC] 2101 npc/2101.lua 開始 (物件 " + strconv.Itoa(int(oid)) + ")",
			"[GM NPC] 2101 npc/2101.lua 第1行 顯示:選單 選項:1,4「Pick」",
			"[GM NPC] 2101 第1行 回應:選項 4",
			"[GM NPC] 2101 npc/2101.lua 第2行 顯示:對話「bye」",
			"[GM NPC] 2101 第2行 回應:下一頁",
			"[GM NPC] 2101 npc/2101.lua 第2行 結束",
		}
		if len(got) != len(want) {
			t.Fatalf("notices %q", got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("notice %d = %q want %q", i, got[i], want[i])
			}
		}
	}
}

func TestGMSeesWhyAnNPCIsUnhandled(t *testing.T) {
	sw, err := newScriptWorld()
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
	var oid int32
	var npcID int
	for id, npc := range room.npcs {
		if npc.TemplateID != 2101 && npc.TemplateID != 2100 {
			oid, npcID = id, npc.TemplateID
		}
	}
	if oid == 0 {
		t.Skip("map 10000 has no unscripted NPC in the embedded catalog")
	}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, SessionID: "gm-npc-missing", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 1, GM: 1, MapID: 10000, X: room.npcs[oid].X, Y: room.npcs[oid].Y}}}
	s := &Server{scripts: sw, gameplay: gameplay}
	sink := &memSink{}
	handled, err := s.startNPCScript(context.Background(), 1, live, sink, oid)
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Skipf("NPC %d has a script", npcID)
	}
	want := protocol.PortalTrainingMessage("[GM NPC] " + strconv.Itoa(npcID) + " 未處理(伺服器沒有 npc/" + strconv.Itoa(npcID) + ".lua 腳本)")
	if len(sink.pkts) != 1 || !bytes.Equal(sink.pkts[0], want) {
		t.Fatalf("notices %q", noticeTexts(sink))
	}
}

func TestGMCommandNoticeCountsArguments(t *testing.T) {
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, Character: domain.Character{ID: 1, GM: 1}}}
	sink := &memSink{}
	a := &worldScript{server: &Server{}, live: live, sink: sink, npcID: 1012104, kind: "npc"}
	command := scripting.Command{Kind: "change_look", Script: "npc/1012104.lua", Line: 27, Ints: []int64{5150000, 1, 30310, 30330, 30060, 30150, 30410}}
	a.gmCommand(command, scripting.CommandResult{Value: map[string]any{"ok": true, "style": int64(30330)}}, nil)
	got := noticeTexts(sink)
	want := "npc/1012104.lua 第27行 指令 change_look(5150000,1,30310,30330,30060,…共7個參數) → 完成 樣式=30330"
	if len(got) != 1 || !strings.HasSuffix(got[0], want) {
		t.Fatalf("notices %q", got)
	}
}
