package world

import (
	"bytes"
	"context"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/scripts"
)

func scriptedQuestClick(quest uint16) *protocol.Reader {
	w := protocol.NewRawWriter()
	w.U8(4)
	w.U16(quest)
	w.I32(1012100)
	w.I16(0)
	w.I16(0)
	return protocol.NewReader(w.Packet())
}

func TestQuestClickNoticeIsGMOnly(t *testing.T) {
	for _, gm := range []int{0, 1} {
		live := &liveCharacter{session: domain.CharacterSession{CharacterID: 7, Character: domain.Character{ID: 7, GM: gm, Quests: []domain.QuestState{{ID: 60000, State: 1}}}}}
		sink := &gameplayTestSink{}
		if err := (&Server{}).handleQuest(context.Background(), 1, live, sink, scriptedQuestClick(60000)); err != nil {
			t.Fatal(err)
		}
		if got := sink.packets[len(sink.packets)-1]; !bytes.Equal(got, protocol.EnableActions()) {
			t.Fatalf("gm=%d last packet=%x", gm, got)
		}
		if gm == 0 {
			if len(sink.packets) != 1 {
				t.Fatalf("player received %d packets", len(sink.packets))
			}
			continue
		}
		want := protocol.PortalTrainingMessage("[GM 任務] ID 60000 NPC 1012100 動作:接受(腳本) 狀態:進行中 結果:未處理(伺服器沒有 quest/60000.lua 腳本)")
		if len(sink.packets) != 3 || !bytes.Equal(sink.packets[0], want) {
			t.Fatalf("gm packets=%d first=%q", len(sink.packets), sink.packets[0])
		}
	}
}

// A generated legacy quest script forces status and grants rewards itself, so
// it must not start unless the WZ requirement check for that phase passes.
func TestLegacyQuestScriptNeedsItsRequirementCheck(t *testing.T) {
	if !scripts.LegacyQuest("quest/2300.lua") {
		t.Fatal("quest 2300 is not a generated legacy source")
	}
	s := &Server{gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{{channel: 1, mapID: 100}: {npcs: map[int32]content.Life{5: {TemplateID: 1012100}}}}}}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 7, ChannelID: 1, Character: domain.Character{ID: 7, GM: 1, MapID: 100}}}
	sink := &gameplayTestSink{}
	// The embedded catalog has no definition for 2300, so nothing can vouch
	// for the player and the script stays closed.
	if err := s.handleQuest(context.Background(), 1, live, sink, scriptedQuestClick(2300)); err != nil {
		t.Fatal(err)
	}
	want := protocol.PortalTrainingMessage("[GM 任務] ID 2300 NPC 1012100 動作:接受(腳本) 狀態:未開始 結果:未處理(無任務資料)")
	if len(sink.packets) < 2 || !bytes.Equal(sink.packets[0], want) || !bytes.Equal(sink.packets[len(sink.packets)-1], protocol.EnableActions()) {
		t.Fatalf("packets=%d first=%q", len(sink.packets), sink.packets[0])
	}
}
