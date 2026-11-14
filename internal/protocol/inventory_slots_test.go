package protocol

import (
	"bytes"
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestNativeInventoryCapacityPackets(t *testing.T) {
	// IDB case0x28 -> sub_936713: Decode1(type), Decode1(new capacity).
	if !bytes.Equal(InventoryCapacityUpdate(1, 28), []byte{0x2b, 0x00, 1, 28}) {
		t.Fatal("native capacity packet mismatch")
	}
	for _, pair := range [][2]int{{0, 28}, {6, 28}, {1, 23}, {1, 97}} {
		if InventoryCapacityUpdate(pair[0], pair[1]) != nil {
			t.Fatal("invalid capacity packet emitted")
		}
	}
	c := domain.Character{ID: 1, Name: "SlotTest", Level: 1, MaxHP: 50, MaxMP: 5, HP: 50, MP: 5}
	w := NewWriter(0)
	addCharacterInfo(w, c, time.Unix(0, 0))
	normal := w.Packet()
	c.InventorySlots = [5]int{28, 32, 36, 40, 44}
	w = NewWriter(0)
	addCharacterInfo(w, c, time.Unix(0, 0))
	expanded := w.Packet()
	differences := 0
	for i := range normal {
		if normal[i] != expanded[i] {
			differences++
		}
	}
	if len(normal) != len(expanded) || differences != 5 || !bytes.Contains(expanded, []byte{28, 32, 36, 40, 44}) {
		t.Fatal("character inventory list capacity encoding changed unrelated bytes")
	}
}
