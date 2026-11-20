package protocol

import (
	"maple-go-server/internal/domain"
	"math"
)

const CharacterCardSaveBodyLength = 4 * len(domain.CharacterCardIDs{})

// ReadCharacterCardSave decodes the six character IDs written by the v148
// Character Card confirmation dialog (0x75FC82). Empty slots are zero.
func ReadCharacterCardSave(r *Reader) (domain.CharacterCardIDs, error) {
	var ids domain.CharacterCardIDs
	if r.Remaining() != CharacterCardSaveBodyLength {
		return ids, ErrMalformed
	}
	for slot := range ids {
		ids[slot] = int64(r.I32())
		if ids[slot] < 0 {
			return domain.CharacterCardIDs{}, ErrMalformed
		}
	}
	return ids, r.Err()
}

func addCharacterCardStats(w *Writer, cards domain.CharacterCards) {
	for _, card := range cards {
		w.I32(int32(card.CharacterID))
		w.U8(uint8(min(card.Level, math.MaxUint8)))
		w.I32(int32(card.Job))
	}
}
