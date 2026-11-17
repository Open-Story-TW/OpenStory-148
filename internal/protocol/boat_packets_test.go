package protocol

import (
	"bytes"
	"testing"
)

func TestBoatPackets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want []byte
	}{
		{"depart", BoatDepart(), []byte{0xE6, 0, 8, 2}},
		{"arrive", BoatArrive(), []byte{0xE6, 0, 12, 6}},
		{"invasion", BoatInvadersArrive(), []byte{0xE6, 0, 10, 4}},
		{"clear invasion", BoatInvadersLeave(), []byte{0xE6, 0, 10, 5}},
		{"docked", BoatState(true, false), []byte{0xE7, 0, 1, 0}},
		{"away", BoatState(false, false), []byte{0xE7, 0, 2, 0}},
		{"boarded", BoatState(false, true), []byte{0xE7, 0, 4, 1}},
	} {
		if !bytes.Equal(tc.got, tc.want) {
			t.Errorf("%s = %x, want %x", tc.name, tc.got, tc.want)
		}
	}
}
