package protocol

import (
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

func TestV148InnerAbilitySection(t *testing.T) {
	w := NewWriter(0x6e)
	lines := domain.InnerAbilities{{}, {ID: 70000008, Level: 3, Grade: 1}, {ID: 71000013, Level: 3, Grade: 1}}
	addInnerAbilityStats(w, lines)
	if got := hex.EncodeToString(w.Packet()); got != "6e00010002881d2c040301" {
		t.Fatalf("unexpected native ability section %s", got)
	}
}
