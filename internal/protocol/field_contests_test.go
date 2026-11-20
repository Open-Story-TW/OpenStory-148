package protocol

import (
	"encoding/hex"
	"testing"
)

func TestFieldContestPacketLayouts(t *testing.T) {
	damage := &[3]int{10, 15, 45}
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"initial snowball", SnowballState(1, [2]int{100, 91}, [2]int{0, 45}, damage), "f40101640000005b0000000000082d00080a000f002d00"},
		{"snowball hit", SnowballHit(3, 45, 348), "f501032d005c01"},
		{"snowball message", SnowballMessage(1, 4), "f6010104"},
		{"all coconuts", CoconutAction(-32768, 0, 0), "f8010080000000"},
		{"coconut score", CoconutScore([2]int{13, 9}), "f9010d000900"},
	}
	for _, c := range cases {
		if got := hex.EncodeToString(c.data); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	target, hit, delay, err := ReadSnowballHit(NewReader([]byte{2, 15, 0, 44, 1}))
	if err != nil || target != 2 || hit != 15 || delay != 300 {
		t.Fatalf("snowball hit: %d %d %d %v", target, hit, delay, err)
	}
	target, delay, err = ReadCoconutHit(NewReader([]byte{10, 1, 44, 1}))
	if err != nil || target != 266 || delay != 300 {
		t.Fatalf("coconut hit: %d %d %v", target, delay, err)
	}
	if _, _, err = ReadCoconutHit(NewReader([]byte{0, 128, 44, 1})); err == nil {
		t.Fatal("a player cannot target all coconuts")
	}
}
