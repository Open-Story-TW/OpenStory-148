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

// OXQuiz displays or reveals a question from the client's Etc.wz. The v148
// dispatcher routes 0xE3 to a byte, byte, uint16 decoder.
func OXQuiz(ask bool, set byte, question uint16) []byte {
	w := NewWriter(0xE3)
	if ask {
		w.U8(1)
	} else {
		w.U8(0)
	}
	w.U8(set)
	w.I16(int16(question))
	return w.Packet()
}
