package protocol

import (
	"bytes"
	"testing"
)

func TestOpenGatePackets(t *testing.T) {
	got := OpenGateCreated(4911, -120, 35, true, true, 77)
	want := []byte{0xeb, 0x01, 0, 0x2f, 0x13, 0, 0, 0x88, 0xff, 35, 0, 1, 77, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("created % x", got)
	}
	if got := OpenGateRemoved(4911, false, false); !bytes.Equal(got, []byte{0xec, 0x01, 1, 0x2f, 0x13, 0, 0, 0}) {
		t.Fatalf("removed % x", got)
	}
	u, err := ReadUseOpenGate(NewReader([]byte{0x2f, 0x13, 0, 0, 0x88, 0xff, 35, 0, 1}))
	if err != nil || u != (UseOpenGate{OwnerID: 4911, X: -120, Y: 35, FirstList: true}) {
		t.Fatal(u, err)
	}
	for _, bad := range [][]byte{{0x2f, 0x13, 0, 0, 0, 0, 0, 0, 2}, {0x2f, 0x13, 0, 0, 0, 0, 0, 0}, {0, 0, 0, 0, 0, 0, 0, 0, 1}} {
		if _, err := ReadUseOpenGate(NewReader(bad)); err == nil {
			t.Fatalf("accepted % x", bad)
		}
	}
}
