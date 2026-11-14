package world

import (
	"bytes"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type panelSink struct{ packets [][]byte }

func (s *panelSink) WritePacket(p []byte) error { s.packets = append(s.packets, p); return nil }

func TestPanelChat(t *testing.T) {
	for text, want := range map[string]bool{"@面板": true, "@panel": true, "@PANEL": true, "@面板 1": false, "面板": false, "@fps 60": false} {
		sink := &panelSink{}
		handled, err := handlePanelChat(sink, text)
		if err != nil || handled != want || (len(sink.packets) == 1) != want {
			t.Errorf("%q: handled=%v packets=%d err=%v", text, handled, len(sink.packets), err)
		}
		if want && len(sink.packets[0]) == 0 {
			t.Errorf("%q: the demo panel did not encode", text)
		}
	}
}

func TestPanelClick(t *testing.T) {
	s := &Server{}
	live := &liveCharacter{session: domain.CharacterSession{Character: domain.Character{Name: "Tester"}}}
	click := func(window, button uint16) [][]byte {
		sink := &panelSink{}
		body := []byte{1, byte(window), byte(window >> 8), byte(button), byte(button >> 8)}
		if err := s.handlePanelClick(live, sink, protocol.NewReader(body)); err != nil {
			t.Fatalf("click %d/%d: %v", window, button, err)
		}
		return sink.packets
	}
	first, second := click(demoPanelWindow, demoPanelHello), click(demoPanelWindow, demoPanelHello)
	if len(first) != 1 || len(second) != 1 || len(first[0]) == 0 || bytes.Equal(first[0], second[0]) {
		t.Fatalf("each press must replace the content with a new count: % x / % x", first, second)
	}
	if got := click(demoPanelWindow, demoPanelClose); len(got) != 1 || !bytes.Equal(got[0], protocol.PanelClosePacket(demoPanelWindow)) {
		t.Fatalf("close = % x", got)
	}
	if got := click(demoPanelWindow, 99); len(got) != 0 {
		t.Fatalf("unknown button answered: % x", got)
	}
	if got := click(2, demoPanelHello); len(got) != 0 {
		t.Fatalf("unknown window answered: % x", got)
	}
	if err := s.handlePanelClick(live, &panelSink{}, protocol.NewReader([]byte{9, 1, 0, 1, 0})); err == nil {
		t.Fatal("a malformed click must fail")
	}
}
