package protocol

import "maple-go-server/internal/domain"

// Mask fields are encoded in native stat-bit order, with no LEVEL field. The
// client decodes SP with the job from this same packet.
func JobAdvancementStats(c domain.Character) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0xffe0)
	w.U16(uint16(c.Job))
	for _, stat := range []int{c.STR, c.DEX, c.INT, c.LUK} {
		writeStat(w, stat)
	}
	for _, stat := range []int{c.HP, c.WireMaxHP(), c.MP, c.WireMaxMP()} {
		w.I32(int32(stat))
	}
	writeStat(w, c.AP)
	writeSP(w, c)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

// writeSP follows GW_CharacterStat (0x4C2AA6): extended-SP jobs send a
// count and (book+1, sp) pairs for non-empty books, others a U16 pool.
func writeSP(w *Writer, c domain.Character) {
	if !domain.ExtendedSPJob(c.Job) {
		w.U16(uint16(c.SP))
		return
	}
	table := c.SPTable()
	count := 0
	for _, sp := range table {
		if sp > 0 {
			count++
		}
	}
	w.U8(byte(count))
	for i, sp := range table {
		if sp > 0 {
			w.U8(byte(i + 1))
			w.U8(byte(min(sp, 255)))
		}
	}
}
