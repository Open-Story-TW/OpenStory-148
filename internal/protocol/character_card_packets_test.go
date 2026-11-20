package protocol

import (
	"encoding/hex"
	"errors"
	"maple-go-server/internal/domain"
	"testing"
)

func TestV148CharacterCardRequestAndStatSlots(t *testing.T) {
	// CUICharacterCard writes six IDs. Empty slots remain distinct from gaps
	// in the card list, including when the second deck alone contains a card.
	body, err := hex.DecodeString("010000000000000001120000000000000200000000000000")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ReadCharacterCardSave(NewReader(body))
	if err != nil || ids != (domain.CharacterCardIDs{1, 0, 4609, 0, 2, 0}) {
		t.Fatalf("got %v, %v", ids, err)
	}
	for _, malformed := range [][]byte{body[:23], append(append([]byte(nil), body...), 0)} {
		if _, err = ReadCharacterCardSave(NewReader(malformed)); !errors.Is(err, ErrMalformed) {
			t.Fatalf("invalid length %d returned %v", len(malformed), err)
		}
	}
	negative := append([]byte(nil), body...)
	negative[3] = 0x80
	if _, err = ReadCharacterCardSave(NewReader(negative)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("negative character ID returned %v", err)
	}
	w := NewRawWriter()
	addCharacterCardStats(w, domain.CharacterCards{
		{CharacterID: 1, Level: 120, Job: 112}, {}, {}, {CharacterID: 2, Level: 200, Job: 222},
	})
	data := NewReader(w.Packet())
	if data.Remaining() != 54 || data.I32() != 1 || data.U8() != 120 || data.I32() != 112 {
		t.Fatal("first v148 card stat slot differs")
	}
	data.Skip(18)
	if data.I32() != 2 || data.U8() != 200 || data.I32() != 222 || data.Remaining() != 18 {
		t.Fatal("second deck's original 9-byte slot differs")
	}
}
