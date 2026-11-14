package protocol

// SendStatDetail carries the derived numbers the launcher draws in the extra
// column of the stat window's detail page. Like SendClientConfig it is a
// launcher-only frame: the launcher consumes it and the native client never
// sees the opcode.
const (
	SendStatDetail uint16 = 0x614

	statDetailVersion = 1
)

// StatDetail is one character's derived numbers, in wire order. Rates are
// percents with 100 as the unmodified rate; the other fields are percentage
// points except DamageCap.
type StatDetail struct {
	ExpRate, DropRate, MesoRate, PointRate int32
	DamageCap                              int64 // Per-line damage cap.
	CritDamage                             int32
	TotalDamage                            int32
	BossDamage                             int32
	IgnoreDefense                          int32
	ReflectDamage, ReflectChance           int32
	HPSteal, MPSteal                       int32
	HPStealChance, MPStealChance           int32
	StatusResist, ElementResist            int32
	DamageReduce                           int32
	AvoidChance                            int32
}

// StatDetailPacket encodes d: the version byte and 81 payload bytes in all.
func StatDetailPacket(d StatDetail) []byte {
	w := NewWriter(SendStatDetail)
	w.U8(statDetailVersion)
	w.I32(d.ExpRate)
	w.I32(d.DropRate)
	w.I32(d.MesoRate)
	w.I32(d.PointRate)
	w.I64(d.DamageCap)
	for _, v := range []int32{d.CritDamage, d.TotalDamage, d.BossDamage, d.IgnoreDefense, d.ReflectDamage, d.ReflectChance,
		d.HPSteal, d.MPSteal, d.HPStealChance, d.MPStealChance, d.StatusResist, d.ElementResist, d.DamageReduce, d.AvoidChance} {
		w.I32(v)
	}
	return w.Packet()
}
