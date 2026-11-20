package protocol

import (
	"bytes"
	"testing"
)

func TestBattleSquareNativeLobbyPackets(t *testing.T) {
	if got := OpenBattleSquare(); !bytes.Equal(got, []byte{0x62, 1, 50, 0, 0, 0}) {
		t.Fatalf("open launcher: %x", got)
	}
	packet := BattleSquareList(2)
	want := append([]byte{0xf5, 0, 1, 2, 0, 0, 0}, make([]byte, 100)...)
	want = append(want, 100, 0, 0)
	if !bytes.Equal(packet, want) {
		t.Fatalf("availability: %x", packet)
	}
}

func TestReadBattleSquareEntryNativeLayout(t *testing.T) {
	body := []byte{0x78, 0x56, 0x34, 0x12, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	q, err := ReadBattleSquareEntry(NewReader(body))
	if err != nil || q.Tick != 0x12345678 || q.Index != 0 || q.Mode != 0 || q.Rank != 3 || q.Option != 0 || q.Reserved != [2]uint32{} {
		t.Fatalf("entry %+v, err %v", q, err)
	}
	for n := 0; n < len(body); n++ {
		if _, err := ReadBattleSquareEntry(NewReader(body[:n])); err == nil {
			t.Fatalf("accepted truncated body len=%d", n)
		}
	}
	if _, err := ReadBattleSquareEntry(NewReader(append(body, 0))); err == nil {
		t.Fatal("accepted extra byte")
	}
}
