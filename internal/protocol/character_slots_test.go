package protocol

import (
	"encoding/binary"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func slotRoster(n int) []domain.CharacterSummary {
	out := make([]domain.CharacterSummary, n)
	for i := range out {
		out[i] = domain.CharacterSummary{ID: int64(i + 1), Name: fmt.Sprintf("Slot%02d", i), Job: 0, Level: 1, STR: 12, DEX: 5, INT: 4, LUK: 4, HP: 50, MaxHP: 50, BaseMaxHP: 50, MP: 5, MaxMP: 5, BaseMaxMP: 5, MapID: 100000000}
	}
	return out
}

// Sixty characters fit the launcher's list; the slot count follows them, 28 bytes from the end.
func TestCharacterListCarriesUpToSixtyCharacters(t *testing.T) {
	defer domain.SetLevel32ForTest(true)()
	for _, n := range []int{16, 60} {
		packet := CharacterList(slotRoster(n), n)
		if packet == nil || int(packet[3]) != n {
			t.Fatalf("%d characters: packet=%v", n, packet == nil)
		}
		if got := binary.LittleEndian.Uint32(packet[len(packet)-28:]); got != uint32(n) {
			t.Fatalf("%d characters: slots=%d", n, got)
		}
	}
	if CharacterList(slotRoster(61), 61) != nil || CharacterList(slotRoster(3), 61) != nil || CharacterList(slotRoster(3), 0) != nil {
		t.Fatal("list beyond sixty slots accepted")
	}
}
