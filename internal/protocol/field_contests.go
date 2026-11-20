package protocol

const (
	RecvSnowballHit uint16 = 0x188 // CField_SnowBall attack at 0x54460B.
	RecvCoconutHit  uint16 = 0x18A // CField_Coconut attack at 0x528FBB.

	SendSnowballState   uint16 = 0x1F4
	SendSnowballHit     uint16 = 0x1F5
	SendSnowballMessage uint16 = 0x1F6
	SendSnowballPush    uint16 = 0x1F7
	SendCoconutAction   uint16 = 0x1F8
	SendCoconutScore    uint16 = 0x1F9
)

func ReadSnowballHit(r *Reader) (target, damage, delay int, err error) {
	target, damage, delay = int(r.U8()), int(r.U16()), int(r.U16())
	if r.Err() != nil || r.Remaining() != 0 || target > 3 {
		err = ErrMalformed
	}
	return
}

func ReadCoconutHit(r *Reader) (target, delay int, err error) {
	target, delay = int(r.U16()), int(r.U16())
	if r.Err() != nil || r.Remaining() != 0 || target > 0x7FFF {
		err = ErrMalformed
	}
	return
}

// SnowballState adds the three damage amounts on the first field snapshot.
func SnowballState(mode byte, health, progress [2]int, damage *[3]int) []byte {
	w := NewWriter(SendSnowballState)
	w.U8(mode)
	for _, hp := range health {
		w.I32(int32(hp))
	}
	for _, position := range progress {
		w.I16(int16(position))
		w.U8(8) // Native speed table index 8 moves only when progress changes.
	}
	if damage != nil {
		for _, amount := range *damage {
			w.U16(uint16(amount))
		}
	}
	return w.Packet()
}

func SnowballHit(target, damage, delay int) []byte {
	w := NewWriter(SendSnowballHit)
	w.U8(byte(target))
	w.U16(uint16(damage))
	w.U16(uint16(delay))
	return w.Packet()
}

func SnowballMessage(team, message int) []byte {
	w := NewWriter(SendSnowballMessage)
	w.U8(byte(team))
	w.U8(byte(message))
	return w.Packet()
}

func CoconutAction(target, delay, action int) []byte {
	w := NewWriter(SendCoconutAction)
	w.I16(int16(target))
	w.U16(uint16(delay))
	w.U8(byte(action))
	return w.Packet()
}

func CoconutScore(score [2]int) []byte {
	w := NewWriter(SendCoconutScore)
	w.U16(uint16(score[0]))
	w.U16(uint16(score[1]))
	return w.Packet()
}

// CoconutFieldInfo is the field-specific SetField tail read at 0x527E96.
func CoconutFieldInfo(packet []byte, team byte, seconds int) []byte {
	if packet == nil {
		return nil
	}
	return append(packet, team, byte(seconds), byte(seconds>>8), byte(seconds>>16), byte(seconds>>24))
}
