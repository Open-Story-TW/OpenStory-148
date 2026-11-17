package protocol

const (
	RecvNettDefense = 0x196
	nettWave        = 0x21a
	nettLife        = 0x21b
	nettPoints      = 0x21c
	nettResult      = 0x21e
)

// CField_Defense::OnPacket (v148 0x52AEE0) reads three separate i32 counters.
func NettDefenseLife(life int) []byte {
	w := NewWriter(nettLife)
	w.I32(int32(life))
	return w.Packet()
}

func NettDefenseWave(wave int) []byte {
	w := NewWriter(nettWave)
	w.I32(int32(wave))
	return w.Packet()
}

func NettDefensePoints(points int) []byte {
	w := NewWriter(nettPoints)
	w.I32(int32(points))
	return w.Packet()
}

// v148 0x52A9E5 decodes the clear flag and four result integers, but this
// client only renders the clear/fail image.
func NettDefenseResult(clear bool) []byte {
	w := NewWriter(nettResult)
	w.Bool(clear)
	for i := 0; i < 4; i++ {
		w.I32(0)
	}
	return w.Packet()
}

// NettDefenseWalk relays a single absolute walk fragment for a server-driven
// lane mob. It uses the same monster path packet as native controlled mobs.
func NettDefenseWalk(objectID int32, from, x, y, foothold, duration int) []byte {
	w := &Writer{}
	w.U8(1)
	w.U8(0)
	w.I16(int16(x))
	w.I16(int16(y))
	w.I16(int16((x - from) * 1000 / duration))
	w.I16(0)
	w.I16(int16(foothold))
	w.I16(int16(from))
	w.I16(int16(y))
	w.U8(3)
	w.I16(int16(duration))
	return MonsterMovePacket(MonsterMove{ObjectID: objectID, StartX: int16(from), StartY: int16(y), Fragments: w.Packet()})
}
