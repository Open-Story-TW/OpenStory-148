package protocol

import (
	"bytes"
	"testing"
)

func TestRPSV148Packets(t *testing.T) {
	for _, row := range []struct {
		got, want []byte
	}{
		{RPSScreen(9000019), []byte{0x49, 0x02, 8, 0x53, 0x54, 0x89, 0}},
		{RPSResult(1, 0xff), []byte{0x49, 0x02, 11, 1, 0xff}},
		{RPSState(RPSNewRound), []byte{0x49, 0x02, 12}},
	} {
		if !bytes.Equal(row.got, row.want) {
			t.Errorf("packet %x, want %x", row.got, row.want)
		}
	}
}
