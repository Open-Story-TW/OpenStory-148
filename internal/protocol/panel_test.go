package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestPanelPacket(t *testing.T) {
	got := PanelPacket(Panel{Window: 1, Title: "AB", Text: "測", Buttons: []PanelButton{{ID: 7, Label: "OK"}}})
	want := []byte{
		0x16, 0x06, 1, 1, 1, 0,
		2, 0, 'A', 'B',
		2, 0, 0xB4, 0xFA,
		1,
		7, 0, 2, 0, 'O', 'K',
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("panel = % x, want % x", got, want)
	}
	if got, want := PanelClosePacket(0x0203), []byte{0x16, 0x06, 1, 0, 3, 2}; !bytes.Equal(got, want) {
		t.Fatalf("close = % x, want % x", got, want)
	}
}

func TestPanelPacketLimits(t *testing.T) {
	for name, p := range map[string]Panel{
		"title":   {Title: strings.Repeat("a", PanelMaxTitle+1)},
		"text":    {Text: strings.Repeat("測", PanelMaxText/2+1)},
		"label":   {Buttons: []PanelButton{{Label: strings.Repeat("a", PanelMaxLabel+1)}}},
		"buttons": {Buttons: make([]PanelButton, PanelMaxButtons+1)},
		"big5":    {Title: "😀"},
	} {
		if PanelPacket(p) != nil {
			t.Errorf("%s: over-limit panel was encoded", name)
		}
	}
	if PanelPacket(Panel{Title: strings.Repeat("a", PanelMaxTitle), Text: strings.Repeat("測", PanelMaxText/2), Buttons: make([]PanelButton, PanelMaxButtons)}) == nil {
		t.Error("a panel exactly at the limits must encode")
	}
}

func TestReadPanelClick(t *testing.T) {
	click, err := ReadPanelClick(NewReader([]byte{1, 1, 0, 2, 0}))
	if err != nil || click != (PanelClick{Window: 1, Button: 2}) {
		t.Fatalf("click = %+v, %v", click, err)
	}
	for name, body := range map[string][]byte{
		"version":  {2, 1, 0, 2, 0},
		"short":    {1, 1, 0, 2},
		"trailing": {1, 1, 0, 2, 0, 0},
	} {
		if _, err := ReadPanelClick(NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
