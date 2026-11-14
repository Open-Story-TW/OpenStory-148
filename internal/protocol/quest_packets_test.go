package protocol

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestQuestInfoNativeMode10WordBoundary(t *testing.T) {
	p := QuestInfo(1031, 2101, 10, 65535)
	if len(p) != 11 {
		t.Fatalf("native quest-result length=%d, want11", len(p))
	}
	r := NewReader(p)
	if r.U16() != SendQuestInfo || r.U8() != 10 || r.U16() != 1031 || r.I32() != 2101 || r.U16() != 65535 || r.Remaining() != 0 || r.Err() != nil {
		t.Fatal("quest mode10 field widths differ from native decoder")
	}
	if QuestInfo(1031, 2101, 10, 65536) != nil || QuestInfo(65536, 2101, 10, 0) != nil {
		t.Fatal("out-of-range quest ID was silently truncated")
	}
}

func TestQuestActionExactLayouts(t *testing.T) {
	for _, tc := range []struct {
		action      uint8
		coordinates bool
	}{{1, true}, {1, false}, {2, true}, {2, false}, {3, false}, {4, true}, {5, true}} {
		action := tc.action
		w := NewRawWriter()
		w.U8(action)
		w.U16(1031)
		if action != 3 {
			w.I32(2101)
		}
		if action == 1 || action == 2 {
			w.I32(123)
		}
		if tc.coordinates {
			w.I16(-123)
			w.I16(456)
		}
		if action == 2 {
			w.I32(0)
		}
		packet := w.Packet()
		q, e := ReadQuestAction(NewReader(packet))
		if e != nil || q.QuestID != 1031 || q.Action != action {
			t.Fatalf("action%d: %+v %v", action, q, e)
		}
		if tc.coordinates && (q.X != -123 || q.Y != 456) {
			t.Fatalf("native quest coordinates lost: %+v", q)
		}
		if _, e = ReadQuestAction(NewReader(append(packet, 1))); e == nil {
			t.Fatal("trailing data accepted")
		}
		if _, e = ReadQuestAction(NewReader(packet[:len(packet)-1])); e == nil {
			t.Fatal("truncated packet accepted")
		}
	}
}
func TestQuestInitialAndLiveTimeFormats(t *testing.T) {
	completed := domain.QuestState{ID: 1031, State: 2, CompletedAt: time.Unix(1730000000, 0)}
	started := domain.QuestState{ID: 1018, State: 1, MobKills: map[int]int{3: 2, 1: 7}, MobOrder: []int{3, 1}}
	w := NewRawWriter()
	AddQuestSections(w, []domain.QuestState{completed, started}, nil)
	r := NewReader(w.Packet())
	if r.U8() != 1 || r.U16() != 1 || r.U16() != 1018 || r.String() != "002007" {
		t.Fatal("started layout")
	}
	if r.U8() != 1 || r.U16() != 1 || r.U16() != 1031 || r.I64() != 31139887 || r.Remaining() != 0 {
		t.Fatal("completed timestamp/layout")
	}
	r = NewReader(QuestUpdate(completed))
	if r.U16() != 0x36 || r.U8() != 1 || r.U16() != 1031 || r.U8() != 2 || r.I64() != mapleTime(completed.CompletedAt) {
		t.Fatal("live timestamp/layout")
	}
}
