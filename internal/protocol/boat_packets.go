package protocol

const RecvBoatStateRequest uint16 = 0x198

// CField_ContiMove dispatches these packets on the docks and exposed decks.
const (
	sendBoatMove  uint16 = 0xE6
	sendBoatState uint16 = 0xE7
)

func BoatDepart() []byte         { return boatMove(8, 2) }
func BoatArrive() []byte         { return boatMove(12, 6) }
func BoatInvadersArrive() []byte { return boatMove(10, 4) }
func BoatInvadersLeave() []byte  { return boatMove(10, 5) }

func boatMove(effect, phase byte) []byte {
	w := NewWriter(sendBoatMove)
	w.U8(effect)
	w.U8(phase)
	return w.Packet()
}

func BoatState(docked, invaded bool) []byte {
	w := NewWriter(sendBoatState)
	switch {
	case invaded:
		w.U8(4)
		w.U8(1)
	case docked:
		w.U8(1)
		w.U8(0)
	default:
		w.U8(2)
		w.U8(0)
	}
	return w.Packet()
}
