package protocol

// Mechanic Open Gate (35101005) per COpenGatePool::OnPacket 0x649831:
// 0x1EB created (0x649339), 0x1EC removed (0x648E1D). The use request 0xE8
// is sent by 0x648B2B when the user presses up at a usable gate.
const (
	SendOpenGateCreated uint16 = 0x1eb
	SendOpenGateRemoved uint16 = 0x1ec
	RecvUseOpenGate     uint16 = 0xe8
)

// OpenGateCreated: U8 state (0 plays the creation animation; non-zero draws
// the gate and links it to the owner's other gate), owner I32, I16 x, I16 y,
// U8 which gate (0 first, 1 second), party I32 (0x649339).
func OpenGateCreated(ownerID int64, x, y int16, second, animated bool, partyID int64) []byte {
	w := NewWriter(SendOpenGateCreated)
	w.Bool(!animated)
	w.I32(int32(ownerID))
	w.I16(x)
	w.I16(y)
	w.Bool(second)
	w.I32(int32(partyID))
	return w.Packet()
}

// OpenGateRemoved: U8 state (0 animated), owner I32, U8 which gate (0x648E1D).
func OpenGateRemoved(ownerID int64, second, animated bool) []byte {
	w := NewWriter(SendOpenGateRemoved)
	w.Bool(!animated)
	w.I32(int32(ownerID))
	w.Bool(second)
	return w.Packet()
}

type UseOpenGate struct {
	OwnerID int64
	X, Y    int16
	// FirstList is the client's own list flag: 1 when the gate it stood at
	// came from its first gate list, 0 from the second.
	FirstList bool
}

// ReadUseOpenGate follows 0x648B2B: owner I32, the gate's I16 x and I16 y,
// U8 list flag.
func ReadUseOpenGate(r *Reader) (UseOpenGate, error) {
	owner := int64(r.I32())
	x, y := r.I16(), r.I16()
	flag := r.U8()
	if r.Err() != nil || r.Remaining() != 0 || owner <= 0 || flag > 1 {
		return UseOpenGate{}, ErrMalformed
	}
	return UseOpenGate{OwnerID: owner, X: x, Y: y, FirstList: flag == 1}, nil
}
