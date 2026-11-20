package protocol

import "maple-go-server/internal/domain"

// Summons (S0b). Output follows CSummonedPool::OnPacket 0x6E8A59: every
// packet starts with the owner I32; create is 0x6E88A1 + CSummoned 0x6E58A2,
// attack 0x6E0F4E, hit 0x6D9F4C. Input follows Legacy SummonHandler (TMS,
// GameConstants.GMS=false) and is confirmed per family by MCP observation.
const (
	RecvMoveSummon   uint16 = 0x11f
	RecvSummonAttack uint16 = 0x120
	RecvDamageSummon uint16 = 0x121
	RecvSubSummon    uint16 = 0x122
	RecvRemoveSummon uint16 = 0x123

	SendSpawnSummon  uint16 = 0x1a1
	SendRemoveSummon uint16 = 0x1a2
	SendMoveSummon   uint16 = 0x1a3
	SendSummonAttack uint16 = 0x1a4
	// 0x188 is the owner/oid/U8-action handler (0x6D9EFB); Legacy's
	// SUMMON_SKILL=0x187 reaches a position/I32 reader instead.
	SendSummonSkill  uint16 = 0x1a7
	SendDamageSummon uint16 = 0x1aa
)

// Summon remove reasons (CSummoned leave types used by Legacy).
const (
	SummonLeaveNone      byte = 1
	SummonLeaveAnimated  byte = 4
	SummonLeaveMech      byte = 5
	SummonLeaveSatellite byte = 10
)

type SummonView struct {
	OwnerID     int64
	ObjectID    int32
	SkillID     int
	OwnerLevel  int
	SkillLevel  int
	X, Y        int16
	Stance      byte
	Foothold    int16
	MoveAbility byte
	AssistType  byte
	Animated    bool
	// Look is the owner avatar for Mirrored Target 4341006 (Java addCharLook);
	// 0x6E58A2 decodes AvatarLook when the flag byte is set.
	Look *domain.Character
}

// SpawnSummon writes the create packet (Java CField.spawnSummon).
func SpawnSummon(v SummonView) []byte {
	if v.OwnerID <= 0 || v.ObjectID <= 0 || v.SkillID <= 0 || v.SkillLevel < 1 || v.SkillLevel > 255 || v.OwnerLevel < 1 || v.OwnerLevel > domain.MaxWireLevel() || v.SkillID == 4341006 && v.Look == nil {
		return nil
	}
	w := NewWriter(SendSpawnSummon)
	w.I32(int32(v.OwnerID))
	w.I32(v.ObjectID)
	w.I32(int32(v.SkillID))
	w.U8(domain.WireLevelByte(v.OwnerLevel) - 1)
	w.U8(byte(v.SkillLevel))
	w.I16(v.X)
	w.I16(v.Y)
	w.U8(v.Stance)
	w.I16(v.Foothold)
	w.U8(v.MoveAbility)
	w.U8(v.AssistType)
	w.Bool(v.Animated)
	w.U8(1)
	if v.SkillID == 4341006 {
		w.U8(1)
		addCharLook(w, *v.Look)
	} else {
		w.U8(0) // No avatar look.
	}
	if v.SkillID == 35111002 {
		w.U8(0) // Tesla Coil: no triangle (Java writes 0; 0x6E58A2 reads 3 points only for 1).
	}
	return w.Packet()
}

func RemoveSummon(ownerID int64, objectID int32, reason byte) []byte {
	w := NewWriter(SendRemoveSummon)
	w.I32(int32(ownerID))
	w.I32(objectID)
	w.U8(reason)
	return w.Packet()
}

func MoveSummon(ownerID int64, objectID int32, startX, startY int16, fragments []byte) []byte {
	if len(fragments) < 1 {
		return nil
	}
	w := NewWriter(SendMoveSummon)
	w.I32(int32(ownerID))
	w.I32(objectID)
	w.I16(startX)
	w.I16(startY)
	w.I32(0)
	w.Bytes(fragments)
	return w.Packet()
}

type SummonHit struct {
	ObjectID int32
	Damage   int
}

// SummonAttackPacket: owner, summon oid, U8 owner level-1, U8 action
// (bit7 facing), U8 count, per target oid/hit action/I32 damage, U8 flag.
func SummonAttackPacket(ownerID int64, objectID int32, ownerLevel int, action byte, hits []SummonHit) []byte {
	return SummonAttackPacketLong(ownerID, objectID, ownerLevel, action, hits, nil)
}

// SummonAttackPacketLong also announces lines above the 32-bit range.
func SummonAttackPacketLong(ownerID int64, objectID int32, ownerLevel int, action byte, hits []SummonHit, long *LongDamageOut) []byte {
	if len(hits) > 15 || ownerLevel < 1 {
		return nil
	}
	w := NewWriter(SendSummonAttack)
	w.I32(int32(ownerID))
	w.I32(objectID)
	w.U8(domain.WireLevelByte(ownerLevel) - 1)
	w.U8(action)
	w.U8(byte(len(hits)))
	for _, h := range hits {
		if h.ObjectID <= 0 || h.Damage < 0 {
			return nil
		}
		w.I32(h.ObjectID)
		w.U8(7)
		w.U32(long.Wire(h.Damage))
	}
	w.U8(0)
	return w.Packet()
}

// DamageSummonPacket: attack index (-1 body touch), damage, and for index>-2
// the monster template and facing.
func DamageSummonPacket(ownerID int64, objectID int32, attackIndex int8, damage int, mobTemplate int, left bool) []byte {
	w := NewWriter(SendDamageSummon)
	w.I32(int32(ownerID))
	w.I32(objectID)
	w.U8(byte(attackIndex))
	w.I32(int32(damage))
	if attackIndex > -2 {
		w.I32(int32(mobTemplate))
		w.Bool(left)
	}
	return w.Packet()
}

type SummonMoveInput struct {
	ObjectID       int32
	StartX, StartY int16
	X, Y           int
	Fragments      []byte
	Changed        bool
}

func ReadSummonMove(r *Reader) (SummonMoveInput, error) {
	var m SummonMoveInput
	m.ObjectID = r.I32()
	m.StartX, m.StartY = r.I16(), r.I16()
	r.Skip(4)
	start := r.Pos()
	x, y, ok := parseAbsolutePosition(r)
	if r.Err() != nil || m.ObjectID <= 0 {
		return m, ErrMalformed
	}
	m.Fragments = r.BytesBetween(start, r.Pos())
	r.Skip(r.Remaining())
	m.X, m.Y, m.Changed = int(x), int(y), ok
	return m, nil
}

type SummonAttackInput struct {
	ObjectID    int32
	Tick        uint32
	Action      byte
	X, Y        int16          // Client summon position at attack time.
	ChargeGauge uint32         // SG-88's original v148 sender writes its charge here.
	Targets     []AttackTarget // Each mob appears once, with its ordered damage lines.
	HitOrder    []int          // Target index for each original wire record, including repeat hits.
	Wire        []byte
}

// ReadSummonAttack decodes the original v148 0x120 sender. Each wire record
// is one hit. A later line against the same mob repeats its object ID after
// the other mobs in that attack wave, as seen with Mechanic satellites.
func ReadSummonAttack(r *Reader) (SummonAttackInput, error) {
	var a SummonAttackInput
	start := r.Pos()
	a.ObjectID = r.I32()
	a.Tick = r.U32()
	a.Action = r.U8()
	n := int(r.U8())
	a.X, a.Y = r.I16(), r.I16()
	r.Skip(4) // Second position pair.
	a.ChargeGauge = r.U32()
	if n > 15 || a.ObjectID <= 0 {
		return a, ErrMalformed
	}
	indices := make(map[int32]int, n)
	for i := 0; i < n; i++ {
		id := r.I32()
		// v148 CSummoned attack sender (0x72EEFC) puts the mob template ID
		// right after the object ID, ahead of the 18 v145 metadata bytes.
		r.Skip(4 + 18)
		raw := r.U32()
		r.Skip(4)
		if r.Err() != nil || id <= 0 {
			return a, ErrMalformed
		}
		index, exists := indices[id]
		if !exists {
			index = len(a.Targets)
			indices[id] = index
			a.Targets = append(a.Targets, AttackTarget{ObjectID: id})
		}
		a.Targets[index].Lines = append(a.Targets[index].Lines, AttackLine{Raw: raw, Damage: int(raw & 0x7fffffff)})
		a.HitOrder = append(a.HitOrder, index)
	}
	if r.Err() != nil {
		return a, ErrMalformed
	}
	r.Skip(r.Remaining())
	a.Wire = r.BytesBetween(start, r.Pos())
	return a, nil
}

type SummonDamageInput struct {
	ObjectID    int32
	AttackIndex int8
	Damage      int
	MobTemplate int
}

// Native CSummoned0x6DB45D constructs0x103 and Encode4(this[52]) only.
func ReadRemoveSummon(r *Reader) (int32, error) {
	id := r.I32()
	if r.Err() != nil || r.Remaining() != 0 || id <= 0 {
		return 0, ErrMalformed
	}
	return id, nil
}

func ReadSummonDamage(r *Reader) (SummonDamageInput, error) {
	d := SummonDamageInput{ObjectID: r.I32()}
	d.AttackIndex, d.Damage, d.MobTemplate = int8(r.U8()), int(r.I32()), int(r.I32())
	r.Skip(r.Remaining())
	if r.Err() != nil || d.Damage < 0 || d.ObjectID <= 0 {
		return d, ErrMalformed
	}
	return d, nil
}

// SummonSkillPacket plays a summon skill animation (Beholder heal/buff).
func SummonSkillPacket(ownerID int64, objectID int32, action byte) []byte {
	w := NewWriter(SendSummonSkill)
	w.I32(int32(ownerID))
	w.I32(objectID)
	w.U8(action)
	return w.Packet()
}

type SubSummonInput struct {
	ObjectID int32
	SkillID  int
}

// ReadSubSummon follows SummonHandler.SubSummon: oid, then (Beholder) the
// linked skill ID; trailing bytes are skill-specific and ignored.
func ReadSubSummon(r *Reader) (SubSummonInput, error) {
	in := SubSummonInput{ObjectID: r.I32()}
	if r.Remaining() >= 4 {
		in.SkillID = int(r.I32()) // Absent for some summons (Healing Robot): 0 = the summon's own skill.
	}
	r.Skip(r.Remaining())
	if r.Err() != nil || in.ObjectID <= 0 || in.SkillID < 0 {
		return in, ErrMalformed
	}
	return in, nil
}
