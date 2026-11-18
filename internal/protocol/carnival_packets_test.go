package protocol

import (
	"bytes"
	"testing"
)

func TestCarnivalV148Packets(t *testing.T) {
	cases := []struct {
		label   string
		payload []byte
		wire    []byte
	}{
		{"invitation", TimedQuestInvitation(7057, 60000), []byte{0x58, 0, 13, 0x91, 0x1B, 0x60, 0xEA, 0, 0}},
		{"start", CarnivalStart(1, 23, 123, 400, 300, 2),
			[]byte{0xFC, 1, 1, 23, 0, 0, 0, 123, 0, 0, 0, 0x90, 1, 0, 0, 0x2C, 1, 0, 0, 0, 0}},
		{"points", CarnivalCP(23, 123), []byte{0xFD, 1, 23, 0, 0, 0, 123, 0, 0, 0}},
		{"score", CarnivalScore(300, 400), []byte{0xFE, 1, 0x2C, 1, 0, 0, 0x90, 1, 0, 0}},
		{"summoned", CarnivalActionAccepted(0, 3, "A"), []byte{0, 2, 0, 3, 1, 0, 'A'}},
		{"death", CarnivalDeath(1, "A", 100), []byte{2, 2, 1, 1, 0, 'A', 100}},
		{"ranks", CarnivalRanking([]CarnivalRank{{42, "A", 123, 1}}),
			[]byte{5, 2, 1, 0, 42, 0, 0, 0, 1, 0, 'A', 123, 0, 0, 0, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if !bytes.Equal(tc.payload, tc.wire) {
				t.Fatalf("got %x, want %x", tc.payload, tc.wire)
			}
		})
	}
	for _, body := range [][]byte{nil, {1, 1}, {4}, {0xFF}} {
		if _, err := ReadCarnivalAction(body); err == nil {
			t.Fatalf("accepted malformed action %x", body)
		}
	}
	for action := byte(0); action < 4; action++ {
		if got, err := ReadCarnivalAction([]byte{action}); err != nil || got != action {
			t.Fatalf("action %d: got %d, %v", action, got, err)
		}
	}
}
