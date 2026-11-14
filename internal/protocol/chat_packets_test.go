package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNativeGeneralChatLayoutAndBig5(t *testing.T) {
	w := NewRawWriter()
	w.U32(0x12345678)
	w.String("你好")
	w.U8(1)
	input := w.Packet()
	if !bytes.Equal(input, []byte{0x78, 0x56, 0x34, 0x12, 4, 0, 0xa7, 0x41, 0xa6, 0x6e, 1}) {
		t.Fatalf("native send fixture changed: %x", input)
	}
	got, err := ReadGeneralChat(NewReader(input))
	if err != nil || got.ClientTick != 0x12345678 || got.Text != "你好" || !got.Show {
		t.Fatalf("chat decode: %+v %v", got, err)
	}
	packet := GeneralChat(101, got.Text, got.Show)
	if len(packet) != 14 || binary.LittleEndian.Uint16(packet) != SendGeneralChat || binary.LittleEndian.Uint32(packet[2:]) != 101 || packet[6] != 0 || !bytes.Equal(packet[7:], input[4:]) {
		t.Fatalf("native receive fixture changed: %x", packet)
	}
}

func TestGeneralChatRejectsMalformedControlsAndOversizedText(t *testing.T) {
	for _, text := range []string{"line\nline", "nul\x00text", string(bytes.Repeat([]byte{'a'}, 257))} {
		w := NewRawWriter()
		w.U32(1)
		w.String(text)
		w.U8(0)
		if _, err := ReadGeneralChat(NewReader(w.Packet())); err == nil {
			t.Fatalf("accepted invalid chat %q", text)
		}
	}
	w := NewRawWriter()
	w.U32(1)
	w.String("hello")
	w.U8(0)
	packet := w.Packet()
	for _, input := range [][]byte{packet[:len(packet)-1], append(append([]byte(nil), packet...), 0), append(append([]byte(nil), packet[:len(packet)-1]...), 2)} {
		if _, err := ReadGeneralChat(NewReader(input)); err == nil {
			t.Fatalf("accepted malformed chat %x", input)
		}
	}
}
