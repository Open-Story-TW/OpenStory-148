package protocol

import (
	"bytes"
	"testing"
)

func TestPartyHPNativeThreeInt32LayoutAndZeroMaximumGuard(t *testing.T) {
	want := []byte{0x47, 0x01, 11, 0, 0, 0, 25, 0, 0, 0, 50, 0, 0, 0}
	if got := PartyMemberHP(11, 25, 50); !bytes.Equal(got, want) {
		t.Fatalf("HP packet%x", got)
	}
	if p := PartyMemberHP(11, 0, 50); len(p) != 14 {
		t.Fatal("deadHP0 rejected")
	}
	for _, c := range []struct {
		id      int64
		hp, max int
	}{{0, 1, 50}, {11, 0, 0}, {11, -1, 50}, {11, 51, 50}, {11, 1, 10_000_000}} {
		if PartyMemberHP(c.id, c.hp, c.max) != nil {
			t.Fatal("unsafe HP packet", c)
		}
	}
}
