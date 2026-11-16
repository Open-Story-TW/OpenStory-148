package protocol

// Clock shows the field countdown timer. The v148 field dispatcher at
// 0x51F0E7 routes 0xE5 to the clock handler at 0x507894; type 2 shows the
// remaining time in seconds.
func Clock(seconds int) []byte {
	if seconds < 0 {
		seconds = 0
	}
	w := NewWriter(0xE5)
	w.U8(2)
	w.I32(int32(seconds))
	return w.Packet()
}
