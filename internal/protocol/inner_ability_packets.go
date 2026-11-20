package protocol

import "maple-go-server/internal/domain"

// addInnerAbilityStats writes CharacterData's v148 0x80000000 ability
// section. The decoder at 0x4D6F13 consumes slot, skill, level and grade.
func addInnerAbilityStats(w *Writer, lines domain.InnerAbilities) {
	count := 0
	for _, line := range lines {
		if line.Valid() {
			count++
		}
	}
	w.U16(uint16(count))
	for i, line := range lines {
		if !line.Valid() {
			continue
		}
		w.U8(byte(i + 1))
		w.I32(int32(line.ID))
		w.U8(byte(line.Level))
		w.U8(byte(line.Grade))
	}
}
