package protocol

// Keydown skill preparation (Java SKILL_EFFECT / CANCEL_SKILL_EFFECT). The
// client sends 0x97 when a charge skill starts (Big Bang 2221001, Monster
// Bomb 4341003, Swallow 33101005); others see it through CUserRemote::
// OnSkillPrepare (0x121, 0x8B3476: after the owner, I32 skill, U8 level,
// U16 action/direction, U8 speed). 0x123 ends it (sub_8B3DA8, I32 skill).
const (
	RecvSkillPrepare uint16 = 0xa5

	SendSkillPrepare       uint16 = 0x138
	SendSkillPrepareCancel uint16 = 0x13a
)

type SkillPrepare struct {
	SkillID   int
	Level     byte
	Direction uint16
	Speed     byte
	// Target is Swallow's monster (33101005 appends its object ID).
	Target int32
}

func ReadSkillPrepare(r *Reader) (SkillPrepare, error) {
	p := SkillPrepare{SkillID: int(r.I32()), Level: r.U8(), Direction: r.U16(), Speed: r.U8()}
	if p.SkillID == 33101005 && r.Remaining() >= 4 {
		p.Target = r.I32()
	}
	r.Skip(r.Remaining())
	if r.Err() != nil || p.SkillID <= 0 || p.SkillID >= 91000000 || p.Level == 0 {
		return p, ErrMalformed
	}
	return p, nil
}

func SkillPreparePacket(ownerID int64, p SkillPrepare) []byte {
	w := NewWriter(SendSkillPrepare)
	w.I32(int32(ownerID))
	w.I32(int32(p.SkillID))
	w.U8(p.Level)
	w.U16(p.Direction)
	w.U8(p.Speed)
	return w.Packet()
}

func SkillPrepareCancel(ownerID int64, skillID int) []byte {
	w := NewWriter(SendSkillPrepareCancel)
	w.I32(int32(ownerID))
	w.I32(int32(skillID))
	return w.Packet()
}

// RecvThrowGrenade (0xAB, client sub_86E2F7) releases a thrown keydown skill
// (Monster Bomb 4341003): I32 x, I32 y (grenade origin), I32 character y,
// I32 hold time, I32 skill, U8 facing, I32 action (sub_89B4F8, the attack
// animation, not the level: the packet carries no level), I32 grenade key.
// The client waits for nothing; the server applies the hit at the landing
// point with the learned level.
const RecvThrowGrenade uint16 = 0xb8

type ThrowGrenade struct {
	X, Y, CharacterY int32
	HoldMS           int32
	SkillID          int
	FacingLeft       bool
	Action           int32
	Key              int32
}

func ReadThrowGrenade(r *Reader) (ThrowGrenade, error) {
	g := ThrowGrenade{X: r.I32(), Y: r.I32(), CharacterY: r.I32(), HoldMS: r.I32(), SkillID: int(r.I32())}
	g.FacingLeft = r.U8()&1 != 0
	g.Action, g.Key = r.I32(), r.I32()
	r.Skip(r.Remaining())
	if r.Err() != nil || g.SkillID <= 0 || g.HoldMS < 0 {
		return g, ErrMalformed
	}
	return g, nil
}
