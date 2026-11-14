package protocol

// Evan dragon (Java MapleDragon). CUserPool::OnUserCommonPacket 0x8AE121
// routes 274..276 to 0x83E5A4 after the owner I32: 274 creates the dragon
// (0x4D7684), 275 pushes the packet into its move path (vtable +0xAC); 276
// is routed but ignored by v145 (the dragon leaves with its owner).
const (
	RecvMoveDragon uint16 = 0x127

	SendSpawnDragon  uint16 = 0x129
	SendMoveDragon   uint16 = 0x12a
	SendRemoveDragon uint16 = 0x12b
)

// HasDragon reports the Java MapleDragon jobs (Evan 2200–2218).
func HasDragon(job int) bool { return job >= 2200 && job <= 2218 }

// SpawnDragon is Java CField.spawnDragon: owner, I32 x, I32 y, U8 stance,
// U16 0, U16 job.
func SpawnDragon(ownerID int64, x, y int, stance byte, job int) []byte {
	w := NewWriter(SendSpawnDragon)
	w.I32(int32(ownerID))
	w.I32(int32(x))
	w.I32(int32(y))
	w.U8(stance)
	w.U16(0)
	w.U16(uint16(job))
	return w.Packet()
}

// MoveDragon is Java CField.moveDragon: owner, start position, I32 0 and the
// client movement list.
func MoveDragon(ownerID int64, startX, startY int16, fragments []byte) []byte {
	if len(fragments) < 1 {
		return nil
	}
	w := NewWriter(SendMoveDragon)
	w.I32(int32(ownerID))
	w.I16(startX)
	w.I16(startY)
	w.I32(0)
	w.Bytes(fragments)
	return w.Packet()
}

func RemoveDragon(ownerID int64) []byte {
	w := NewWriter(SendRemoveDragon)
	w.I32(int32(ownerID))
	return w.Packet()
}

type DragonMoveInput struct {
	StartX, StartY int16
	X, Y           int
	Fragments      []byte
	Changed        bool
}

// ReadDragonMove follows Java SummonHandler.MoveDragon: start position, four
// zero bytes, then the movement list (MovementKind 5).
func ReadDragonMove(r *Reader) (DragonMoveInput, error) {
	var m DragonMoveInput
	m.StartX, m.StartY = r.I16(), r.I16()
	r.Skip(4)
	start := r.Pos()
	x, y, ok := parseAbsolutePosition(r)
	if r.Err() != nil {
		return m, ErrMalformed
	}
	m.Fragments = r.BytesBetween(start, r.Pos())
	r.Skip(r.Remaining())
	m.X, m.Y, m.Changed = int(x), int(y), ok
	return m, nil
}

// SendRandomTeleportKey (Java DRAGON_BLINK) is CUserLocal::OnRandomTeleportKey
// (case 356 of 0x8969FF): one byte the client keeps as its Dragon Blink key.
const SendRandomTeleportKey uint16 = 0x17a

func RandomTeleportKey(key int) []byte {
	w := NewWriter(SendRandomTeleportKey)
	w.U8(byte(key))
	return w.Packet()
}

// RecvAranCombo is the client's Aran combo hit report (Java ARAN_COMBO, no
// body); the server answers with testCombo 0x153.
const RecvAranCombo uint16 = 0xfd
