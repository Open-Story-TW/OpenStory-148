package protocol

import (
	"sync"

	"maple-go-server/internal/domain"
)

// Generic user attack wire (S0b). Input opcodes come from the Legacy
// recv.properties; the native sender is integrity-protected, so the input
// layouts follow Java DamageParse and are confirmed per family by MCP wire
// observation. Remote output follows CUserRemote::OnAttack 0x8B9F5E.
const (
	RecvShootAttack uint16 = 0x56
	RecvBodyAttack  uint16 = 0x58

	SendShootAttack uint16 = 0x135
	SendMagicAttack uint16 = 0x136
	SendBodyAttack  uint16 = 0x137
)

// AttackQuirk lists the per-skill extra input fields Java DamageParse reads.
// Unknown bytes are retained in Attack.Wire, never interpreted.
type AttackQuirk struct {
	PreSkip      int  // melee: bytes right after skillID (2221012-style skills)
	EnergyHeader bool // melee: one byte instead of two before the CRC
	Charge       bool // melee/magic: I32 keydown charge
	ChargeExtra  int  // melee: I32 words after the charge (14111022/14111023)
	PreSpeed     int  // melee/shoot: I32 words before speed
	PreTargets   int  // melee: bytes after the header before targets
	KeyDown      bool // shoot: I32 keydown time after the opaque header
	WideHeader   bool // magic: two opaque bytes instead of one before direction
	MesoExplode  bool // melee: 4211006 Meso Explosion target list
}

// poisonBomb (14111006) explodes where the thrown bomb landed.
const poisonBomb = 14111006

// maxAttackAction is the highest action number an attack may name.
const maxAttackAction = 0x1ff

var attackQuirks sync.Map // int skillID -> AttackQuirk

// RegisterAttackQuirk records a skill wire exception. Job skill packages call
// it from their own init only after Java + MCP wire evidence.
func RegisterAttackQuirk(skillID int, q AttackQuirk) { attackQuirks.Store(skillID, q) }

func init() {
	for _, id := range []int{2221012, 36101001, 42120003} {
		RegisterAttackQuirk(id, AttackQuirk{PreSkip: 1})
	}
	for _, id := range []int{2201009, 4341052, 24120055} {
		RegisterAttackQuirk(id, AttackQuirk{EnergyHeader: true})
	}
	// Teleport-mastery and Aran body-pressure carry one extra byte too.
	for _, id := range []int{2111007, 2211007, 2311007, 12111007, 22161005, 32111010, 21101003} {
		RegisterAttackQuirk(id, AttackQuirk{EnergyHeader: true, PreTargets: 1})
	}
	// Thunder Breaker's 15101003 is not in the v148 keydown list 0x4E5740 and
	// its melee frame has no charge word (live frame 2026-11-20).
	for _, id := range []int{24121000, 5201002, 14111006, 4341002, 4341003, 5301001, 31001000, 31101000, 31111005} {
		RegisterAttackQuirk(id, AttackQuirk{Charge: true})
	}
	for _, id := range []int{14111022, 14111023} {
		RegisterAttackQuirk(id, AttackQuirk{ChargeExtra: 1})
	}
	for _, id := range []int{5300007, 5101012, 5081001, 15101010, 24121005} {
		RegisterAttackQuirk(id, AttackQuirk{PreSpeed: 1})
	}
	// 5101004 had four bytes before its targets in v145; the v148 melee sender
	// (0x91A08F) writes none for it, and it is not in the charge list 0x4E5740.
	RegisterAttackQuirk(4211006, AttackQuirk{MesoExplode: true})
	// 5711002 is excluded by v148 0x4E5740; 5921004 (v148 Rapid Fire) is included.
	for _, id := range []int{3121004, 3221001, 5221004, 5721001, 5921004, 13111002, 33121009, 35001001, 35101009, 23121000, 5311002} {
		RegisterAttackQuirk(id, AttackQuirk{KeyDown: true})
	}
	RegisterAttackQuirk(23111001, AttackQuirk{PreSpeed: 3})
	for _, id := range []int{2121001, 2221001, 2321001} {
		RegisterAttackQuirk(id, AttackQuirk{Charge: true})
	}
	for _, id := range []int{12120010, 12110028, 12100028, 12000026} {
		RegisterAttackQuirk(id, AttackQuirk{WideHeader: true})
	}
}

func QuirkFor(skillID int) AttackQuirk {
	if v, ok := attackQuirks.Load(skillID); ok {
		return v.(AttackQuirk)
	}
	return AttackQuirk{}
}

type AttackLine struct {
	Raw               uint32 // Preserve bit31 and low31 damage. A clear bit does not prove a noncritical hit for every sender.
	Damage            int    // Authoritative applied value for remote output.
	Long              int64  // Long damage: the value a token line stands for, -1 if unannounced.
	visualCriticality attackVisualCriticality
}

type attackVisualCriticality uint8

const (
	displayUnspecified attackVisualCriticality = iota
	displayOrdinary
	displayCritical
)

func (l AttackLine) Critical() bool { return l.Raw&0x80000000 != 0 }

// InferCriticalDisplay is the unnegotiated fallback. It preserves the native
// damage word and does not overwrite a complete frame's presentation metadata.
func (l *AttackLine) InferCriticalDisplay() {
	if l.visualCriticality == displayUnspecified {
		l.visualCriticality = displayCritical
	}
}

func (l AttackLine) displayIsCritical() bool {
	return l.visualCriticality == displayCritical || (l.visualCriticality == displayUnspecified && l.Critical())
}

func (l AttackLine) Claimed() int {
	if l.Long != 0 {
		return max(int(l.Long), 0)
	}
	return int(l.Raw & 0x7fffffff)
}

type AttackTarget struct {
	ObjectID int32
	Metadata [18]byte // v148 inserts an i32 after the first three bytes.
	Lines    []AttackLine
	Trailer  [8]byte
}

// Attack is one decoded native user attack of any family.
type Attack struct {
	Opcode        uint16
	Flag, TByte   byte
	SkillID       int
	Charge        int32
	Follows       int32 // Melee final attacks: the skill that triggered this one.
	Direction     byte  // Remote flags byte; facing is display bit15.
	Display       uint16
	Action        byte
	Speed         byte
	Tick          uint32
	Slot, CSStar  int16 // Shoot ammo slot / cash star slot.
	AOE           byte
	Targets       []AttackTarget
	MesoDrops     []int32 // Meso Explosion drop object IDs.
	EruptionAreas []int32 // 2121003 only: explicit Poison Mist object IDs after the magic tail.
	X, Y          int16
	// TailX/TailY is the optional trailing position: Wild Hunter shots,
	// Evan's dragon magic, Poison Bomb's explosion point.
	TailX, TailY int16
	Wire         []byte
}

func (a Attack) TargetCount() int { return int(a.TByte >> 4) }
func (a Attack) LineCount() int   { return int(a.TByte & 15) }

// FacingLeft is the original remote attack header's MoveAction direction bit.
// CUserRemote::OnAttack copies display bit15 to Avatar MoveAction bit0; set
// means the unmirrored, negative-X afterimage box (left), for 0x11D..0x120.
func (a Attack) FacingLeft() bool { return a.Display&0x8000 != 0 }

// ReadAttack decodes 0x4B melee, 0x4C shoot, 0x4D magic and 0x4E body input.
func ReadAttack(op uint16, r *Reader) (Attack, error) {
	a := Attack{Opcode: op}
	start := r.Pos()
	a.Flag = r.U8()
	a.TByte = r.U8()
	a.SkillID = int(r.I32())
	if r.Err() != nil || a.SkillID < 0 || a.SkillID > 99999999 {
		return a, ErrMalformed
	}
	q := QuirkFor(a.SkillID)
	switch op {
	case RecvMeleeAttack, RecvBodyAttack:
		// Body attacks (Tornado, Teleport Mastery, Dark Aura) use the close
		// range layout; Java never parsed 0x4E, so MCP must confirm it.
		r.Skip(q.PreSkip)
		if q.EnergyHeader {
			r.Skip(1)
		} else {
			r.Skip(2)
		}
		r.U16() // nSkillCRC
		if q.Charge {
			a.Charge = r.I32()
		}
		r.Skip(4 * q.ChargeExtra)
		r.U8()
		a.Direction = r.U8()
		a.Display = r.U16()
		// v148 (CUserLocal melee sender 0x91A08F): i32, action, speed,
		// tick, i32, then one more i32 for melee only (body keeps one).
		r.Skip(4)
		a.Action = r.U8()
		r.Skip(4 * q.PreSpeed)
		a.Speed = r.U8()
		a.Tick = r.U32()
		r.U32()
		if op == RecvMeleeAttack {
			// A final attack (client skill type 3) names the skill it follows
			// and then writes one more byte; every other attack writes zero and
			// no byte (v148 sender 0x91EFF5..0x91F054).
			if a.Follows = r.I32(); a.Follows > 0 {
				r.U8()
			}
		}
		if q.MesoExplode {
			return a, readMesoExplosion(r, &a, start)
		}
		r.Skip(q.PreTargets)
	case RecvShootAttack:
		r.Skip(6)
		if q.KeyDown {
			a.Charge = r.I32()
		}
		a.Direction = r.U8()
		a.Display = r.U16()
		r.Skip(5)
		r.Skip(4 * q.PreSpeed)
		// v148's main shoot sender (0x90B36D) puts two more i32 before the
		// speed; the other sender (0x921FC8) does not. Tell them apart by
		// the frame length the rest of the layout must have.
		if extra := r.Remaining() - shootRest(q, a.TByte); extra == 8 || extra == 12 {
			r.Skip(8)
		}
		a.Speed = r.U8()
		a.Tick = r.U32()
		r.U32()
		a.Slot = r.I16()
		a.CSStar = r.I16()
		a.AOE = r.U8()
	case RecvMagicAttack:
		// v148 (0x90FD33): u8, i32, then the keydown charge, then direction.
		r.Skip(5)
		if q.WideHeader {
			r.Skip(1)
		}
		if q.Charge {
			a.Charge = r.I32()
		}
		a.Direction = r.U8()
		a.Display = r.U16()
		r.Skip(5)
		a.Speed = r.U8()
		a.Tick = r.U32()
		r.U32()
	default:
		return a, ErrMalformed
	}
	if err := readAttackTargets(r, &a); err != nil {
		return a, err
	}
	a.X, a.Y = r.I16(), r.I16()
	switch op {
	case RecvMeleeAttack:
		if a.SkillID == poisonBomb && r.Remaining() == 4 {
			// v148 melee sender 0x91F641 appends the bomb's own position
			// (two Encode2) after the attacker's for Poison Bomb.
			a.TailX, a.TailY = r.I16(), r.I16()
		}
	case RecvShootAttack:
		r.Skip(4)
		if r.Remaining() == 4 {
			// Wild Hunter shots (33001000, 33101002, 33121001; MCP 2026-11-02)
			// end with a second I16 position; Java ignores trailing bytes.
			a.TailX, a.TailY = r.I16(), r.I16()
		}
	case RecvMagicAttack:
		tailFlag := r.U8()
		if tailFlag != 0 && r.Remaining() == 4 {
			// Evan magic (22001001, 22150004, 22101000, 22111000; MCP
			// 2026-11-02) flags a trailing I16 position, next to the owner
			// (the dragon that casts it).
			a.TailX, a.TailY = r.I16(), r.I16()
		}
		if a.SkillID == 2121003 {
			// Native8834D8 selects 1..WZ y owned2111003 areas. The actual
			// 2121003 frame appends U8 count and that many I32 object IDs.
			n := int(r.U8())
			if tailFlag != 0 || n < 1 || n > 6 || r.Remaining() != 4*n {
				return a, ErrMalformed
			}
			seen := make(map[int32]bool, n)
			for range n {
				oid := r.I32()
				if oid <= 0 || seen[oid] {
					return a, ErrMalformed
				}
				seen[oid] = true
				a.EruptionAreas = append(a.EruptionAreas, oid)
			}
		}
	}
	// The action is a sanity bound only: v145 stopped at 0x19d, the v148 client
	// sends higher ones for its newer skills (0x1ab for 5111012).
	if r.Err() != nil || r.Remaining() != 0 || a.Display&0x7fff > maxAttackAction {
		return a, ErrMalformed
	}
	a.Wire = r.BytesBetween(start, r.Pos())
	return a, nil
}

// shootRest is the byte count a shoot frame has from its speed byte to the
// end when the header carries no extra words: speed, tick, i32, slot, cash
// star, AOE, the targets, position and the trailing i32.
func shootRest(q AttackQuirk, tbyte byte) int {
	targets := int(tbyte >> 4)
	lines := int(tbyte & 15)
	return 1 + 4 + 4 + 2 + 2 + 1 + targets*(4+18+4*lines+8) + 4 + 4
}

func readAttackTargets(r *Reader, a *Attack) error {
	seen := map[int32]bool{}
	for i := 0; i < a.TargetCount(); i++ {
		t := AttackTarget{ObjectID: r.I32()}
		copy(t.Metadata[:], r.Bytes(18))
		for j := 0; j < a.LineCount(); j++ {
			raw := r.U32()
			t.Lines = append(t.Lines, AttackLine{Raw: raw, Damage: int(raw & 0x7fffffff)})
		}
		copy(t.Trailer[:], r.Bytes(8))
		if r.Err() != nil || t.ObjectID <= 0 || seen[t.ObjectID] {
			return ErrMalformed
		}
		seen[t.ObjectID] = true
		a.Targets = append(a.Targets, t)
	}
	return nil
}

// readMesoExplosion follows DamageParse.parseMesoExplosion: each target has
// 12 opaque bytes, a U8 line count and I64 lines; drops follow the position.
func readMesoExplosion(r *Reader, a *Attack, start int) error {
	if a.LineCount() == 0 {
		r.Skip(4)
		n := int(r.U8())
		for j := 0; j < n; j++ {
			a.MesoDrops = append(a.MesoDrops, r.I32())
			r.Skip(1)
		}
		r.Skip(2)
	} else {
		for i := 0; i < a.TargetCount(); i++ {
			t := AttackTarget{ObjectID: r.I32()}
			r.Skip(12)
			// v145 (live 4211006, 2026-11-02): U8 line count, I32 lines and the
			// usual 8-byte trailer; Java's I64 lines do not apply.
			n := int(r.U8())
			if n > 15 {
				return ErrMalformed
			}
			for j := 0; j < n; j++ {
				raw := r.U32()
				t.Lines = append(t.Lines, AttackLine{Raw: raw, Damage: int(raw & 0x7fffffff)})
			}
			copy(t.Trailer[:], r.Bytes(8))
			a.Targets = append(a.Targets, t)
		}
		a.X, a.Y = r.I16(), r.I16()
		n := int(r.U8())
		for j := 0; j < n; j++ {
			a.MesoDrops = append(a.MesoDrops, r.I32())
			r.Skip(2)
		}
		r.Skip(2)
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return ErrMalformed
	}
	a.Wire = r.BytesBetween(start, r.Pos())
	return nil
}

// RemoteAttack is the server-authored foreign attack, using applied damage.
type RemoteAttack struct {
	Opcode     uint16 // SendMeleeAttack/SendShootAttack/SendMagicAttack/SendBodyAttack
	SkillLevel int
	Mastery    byte
	BulletItem int32 // Shoot: projectile item; other families: charge.
	Attack     Attack
	Long       *LongDamageOut // Announces lines above the 32-bit range.
}

// RemoteAttackPacket follows CUserRemote::OnAttack 0x8B9F5E: tbyte, level,
// skill level[/ID], 3211006 link skill, flags, display; for action<=0x19D
// speed, mastery, I32, per target oid/hit action/lines, shoot position and
// the Big Bang charge.
func RemoteAttackPacket(c domain.Character, q RemoteAttack) []byte {
	a := q.Attack
	switch q.Opcode {
	case SendMeleeAttack, SendShootAttack, SendMagicAttack, SendBodyAttack:
	default:
		return nil
	}
	if c.ID < 1 || c.Level < 1 || c.Level > domain.MaxWireLevel() || a.SkillID != 0 && (q.SkillLevel < 1 || q.SkillLevel > 255) || len(a.Targets) > 15 || a.LineCount() > 15 {
		return nil
	}
	w := NewWriter(q.Opcode)
	w.I32(int32(c.ID))
	w.U8(byte(len(a.Targets)<<4) | a.TByte&15)
	w.U8(domain.WireLevelByte(c.Level))
	if a.SkillID == 0 {
		w.U8(0)
	} else {
		w.U8(byte(q.SkillLevel))
		w.I32(int32(a.SkillID))
	}
	if a.SkillID == 3211006 {
		w.U8(0) // No linked skill is authorized by the server.
	}
	w.U8(a.Direction)
	w.U16(a.Display)
	if a.Display&0x7fff > maxAttackAction {
		return w.Packet()
	}
	w.U8(a.Speed)
	w.U8(q.Mastery)
	w.I32(q.BulletItem)
	for _, t := range a.Targets {
		if t.ObjectID <= 0 {
			return nil
		}
		w.I32(t.ObjectID)
		w.U8(7)
		if a.SkillID == 4211006 {
			w.U8(byte(len(t.Lines)))
		}
		for _, l := range t.Lines {
			if l.Damage < 0 {
				return nil
			}
			v := q.Long.Wire(l.Damage)
			if l.displayIsCritical() {
				v |= 0x80000000
			}
			w.U32(v)
		}
	}
	if q.Opcode == SendShootAttack {
		w.I16(a.X)
		w.I16(a.Y)
	}
	if a.SkillID == 2121001 || a.SkillID == 2221001 || a.SkillID == 2321001 || a.SkillID == 33101007 {
		w.I32(a.Charge)
	}
	return w.Packet()
}

// RemoteAttackOpcode maps an input family to its foreign output.
func RemoteAttackOpcode(in uint16) uint16 {
	switch in {
	case RecvMeleeAttack:
		return SendMeleeAttack
	case RecvShootAttack:
		return SendShootAttack
	case RecvMagicAttack:
		return SendMagicAttack
	case RecvBodyAttack:
		return SendBodyAttack
	}
	return 0
}

const SendGainForce uint16 = 0xf7

type ForceGain struct {
	Count, Color int32
}

// GainForce is CField case 226 (0x50C125): U8 flag, I32 monster oid, then
// per entry U8 1, I32 count, I32 color, terminated by U8 0 (Demon Slayer
// force absorption orbs, Java CField.gainForce).
func GainForce(objectID int32, entries []ForceGain) []byte {
	if objectID <= 0 || len(entries) == 0 || len(entries) > 16 {
		return nil
	}
	w := NewWriter(SendGainForce)
	w.U8(1)
	w.I32(objectID)
	for _, e := range entries {
		w.U8(1)
		w.I32(e.Count)
		w.I32(e.Color)
	}
	w.U8(0)
	return w.Packet()
}
