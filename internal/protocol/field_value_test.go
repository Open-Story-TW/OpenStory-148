package protocol

import (
	"bytes"
	"testing"
)

func TestV148FieldValues(t *testing.T) {
	cases := []struct {
		opcode byte
		encode func(string, string) []byte
	}{
		{0x70, SessionValue},
		{0x71, PartyValue},
		{0x72, MapValue},
	}
	for _, c := range cases {
		want := []byte{c.opcode, 0, 3, 0, 'k', 'e', 'y', 1, 0, 'B'}
		if got := c.encode("key", "B"); !bytes.Equal(got, want) {
			t.Fatalf("opcode %x got %x, want %x", c.opcode, got, want)
		}
	}
}
