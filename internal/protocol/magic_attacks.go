package protocol

import "maple-go-server/internal/domain"

const RecvMagicAttack uint16 = 0x57

type MagicLine struct {
	Raw    uint32 // Bit31 is native critical; lower31 is the displayed damage.
	Damage int    // Authoritative applied line value when used for foreign output.
}
type MagicTarget struct {
	ObjectID int32
	Metadata [18]byte
	Lines    []MagicLine
	Trailer  [8]byte
}
type MagicAttack struct {
	Flag, TByte byte
	SkillID     int
	Validation  [4]byte
	Unknown     byte
	Direction   byte // Not facing: display bit15 carries facing.
	Display     uint16
	Animation   [5]byte
	Speed       byte
	Tick        uint32
	HeaderTail  [4]byte
	Targets     []MagicTarget
	X, Y        int16
	Tail        byte
	Wire        []byte
}

// ReadBeginnerMagicAttack is deliberately limited to the native observed
// 1000 one-line, zero/one-target family (33/67 bytes). Other magic serializers
// and charge/special skill headers are not inferred from this frame.
func ReadBeginnerMagicAttack(r *Reader) (MagicAttack, error) {
	var a MagicAttack
	start := r.Pos()
	a.Flag = r.U8()
	a.TByte = r.U8()
	a.SkillID = int(r.I32())
	if a.SkillID != 1000 || a.TByte&15 != 1 || a.TByte>>4 > 1 {
		return a, ErrMalformed
	}
	copy(a.Validation[:], r.Bytes(4))
	a.Unknown = r.U8()
	a.Direction = r.U8()
	a.Display = r.U16()
	copy(a.Animation[:], r.Bytes(5))
	a.Speed = r.U8()
	a.Tick = r.U32()
	copy(a.HeaderTail[:], r.Bytes(4))
	for i := 0; i < int(a.TByte>>4); i++ {
		t := MagicTarget{ObjectID: r.I32()}
		copy(t.Metadata[:], r.Bytes(18))
		for j := 0; j < int(a.TByte&15); j++ {
			raw := r.U32()
			t.Lines = append(t.Lines, MagicLine{Raw: raw, Damage: int(raw & 0x7fffffff)})
		}
		copy(t.Trailer[:], r.Bytes(8))
		a.Targets = append(a.Targets, t)
	}
	a.X, a.Y, a.Tail = r.I16(), r.I16(), r.U8()
	if r.Err() != nil || r.Remaining() != 0 || a.Display&0x7fff > 0x19d || a.Speed < 1 || a.Speed > 9 {
		return a, ErrMalformed
	}
	for _, t := range a.Targets {
		if t.ObjectID <= 0 {
			return a, ErrMalformed
		}
	}
	a.Wire = r.BytesBetween(start, r.Pos())
	return a, nil
}

// MagicAttackPacket follows CUserRemote::OnAttack8B9F5E case287. The two
// input-only validation blocks, target metadata and final position are not
// remote attack fields. Skill level comes from the authenticated server state.
func MagicAttackPacket(c domain.Character, level int, a MagicAttack) []byte {
	if c.ID < 1 || c.Level < 1 || c.Level > domain.MaxCharacterLevel() || a.SkillID != 1000 || level < 1 || level > 3 || len(a.Targets) > 1 || a.TByte&15 != 1 || a.Display&0x7fff > 0x19d {
		return nil
	}
	w := NewWriter(0x136)
	w.I32(int32(c.ID))
	w.U8(byte(len(a.Targets)<<4) | 1)
	w.U8(domain.WireLevelByte(c.Level))
	w.U8(byte(level))
	w.I32(1000)
	w.U8(a.Direction)
	w.U16(a.Display)
	w.U8(a.Speed)
	w.U8(0)
	w.I32(-1)
	for _, t := range a.Targets {
		if t.ObjectID <= 0 || len(t.Lines) != 1 || t.Lines[0].Damage < 0 || t.Lines[0].Damage > 2147483647 {
			return nil
		}
		w.I32(t.ObjectID)
		w.U8(7)
		w.U32(uint32(t.Lines[0].Damage)) // 1000 does not authorize client critical flags.
	}
	return w.Packet()
}
