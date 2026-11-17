// Gameplay wire layouts derived from Legacy CField, MobPacket, DamageParse and
// PlayerHandler (AGPLv3). Advanced job-specific attack encodings remain rejected.
package protocol

import (
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

const (
	RecvMeleeAttack         uint16 = 0x55
	RecvNPCTalk             uint16 = 0x69
	RecvNPCTalkMore         uint16 = 0x6b
	RecvNPCAction           uint16 = 0x173
	RecvMoveMonster         uint16 = 0x163
	SendMeleeAttack         uint16 = 0x134
	SendSpawnMonster        uint16 = 0x1ab
	SendKillMonster         uint16 = 0x1ac
	SendControlMonster      uint16 = 0x1ad
	SendMoveMonster         uint16 = 0x1ae
	SendMoveMonsterResponse uint16 = 0x1af
	SendDamageMonster       uint16 = 0x1b3
	SendMonsterHP           uint16 = 0x1b9
	SendSpawnNPC            uint16 = 0x1cf
	SendRemoveNPC           uint16 = 0x1d0
	SendNPCController       uint16 = 0x1d2
	SendNPCTalk             uint16 = 0x241
)

type PortalRequest struct {
	TargetMap int
	Name      string
	Revival   *RevivalRequest
}

func ReadPortalRequest(r *Reader) (PortalRequest, error) {
	r.U8()
	req := PortalRequest{TargetMap: int(r.I32()), Name: r.String()}
	if req.TargetMap != -1 && req.Name == "" {
		// CField::SendTransferFieldRequest (native 0x4F972F): a null
		// portal omits coordinates. Wheel is a DWORD, not Java's short.
		v := &RevivalRequest{}
		zero := r.U8()
		v.WheelType = r.U32()
		crc := r.U8()
		if crc == 1 {
			r.Skip(8)
		}
		if r.Err() != nil || r.Remaining() != 0 || zero != 0 || crc > 1 || v.WheelType > 7 {
			return req, ErrMalformed
		}
		req.Revival = v
		return req, nil
	}
	// wheel type and client coordinates/CRC are not authoritative.
	if r.Err() != nil || len(req.Name) > 64 || r.Remaining() > 24 {
		return req, ErrMalformed
	}
	r.Skip(r.Remaining())
	return req, r.Err()
}

func WarpMap(c domain.Character, channel int) []byte { return warpMap(c, channel, false, 0, 0) }

// WarpMapAt places the user at (x, y) instead of the spawn portal: CStage::
// OnSetField reads a chase flag and I32 x/y after HP (0x69EDC2).
func WarpMapAt(c domain.Character, channel, x, y int) []byte { return warpMap(c, channel, true, x, y) }

func warpMap(c domain.Character, channel int, chase bool, x, y int) []byte {
	if channel < 1 || channel > 20 || c.MapID < 0 || c.SpawnPoint < 0 || c.SpawnPoint > 255 {
		return nil
	}
	w := NewWriter(SendSetField)
	w.I32(int32(channel - 1))
	w.U8(0)
	w.I32(0)
	w.U8(2)
	w.I32(0)
	w.U8(0)
	w.U16(0)
	w.U8(0)
	w.I32(int32(c.MapID))
	w.U8(byte(c.SpawnPoint))
	w.I32(int32(c.HP))
	if chase {
		w.U8(1)
		w.I32(int32(x))
		w.I32(int32(y))
	} else {
		w.U8(0)
	}
	w.I64(mapleTime(time.Now()))
	w.I32(100)
	w.U8(0)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

func SpawnNPC(objectID int32, n content.Life, controller bool) []byte {
	op := SendSpawnNPC
	if controller {
		op = SendNPCController
	}
	w := NewWriter(op)
	if controller {
		w.U8(1)
	}
	w.I32(objectID)
	w.I32(int32(n.TemplateID))
	w.I16(int16(n.X))
	w.I16(int16(n.Y))
	w.Bool(n.Facing != 1)
	w.I16(int16(n.Foothold))
	w.I16(int16(n.RX0))
	w.I16(int16(n.RX1))
	w.Bool(!n.Hidden)
	return w.Packet()
}

func RemoveNPC(objectID int32, controller bool) []byte {
	op := SendRemoveNPC
	if controller {
		op = SendNPCController
	}
	w := NewWriter(op)
	if controller {
		w.U8(0)
	}
	w.I32(objectID)
	return w.Packet()
}

func NPCDialogue(templateID int, text string, previous, next bool) []byte {
	w := NewWriter(SendNPCTalk)
	w.U8(4)
	w.I32(int32(templateID))
	w.U8(0)
	w.U8(0)
	w.String(text)
	w.Bool(previous)
	w.Bool(next)
	return w.Packet()
}

func SpawnMonster(objectID int32, life content.Life, controller bool) []byte {
	return SpawnMonsterWithStats(objectID, life, controller, nil, time.Time{})
}

// SpawnMonsterWithStats carries current temporary stats so late joiners and
// new controllers see them (CMobPool::OnMobEnterField -> CMob::TempStatSet).
func SpawnMonsterWithStats(objectID int32, life content.Life, controller bool, stats []MobStatValue, now time.Time) []byte {
	return spawnMonsterWithType(objectID, life, controller, stats, now, -2)
}

// SpawnMonsterProjection shows a stationary event body without exposing it
// as a valid target. Zakum uses it while the arms are still protecting it.
func SpawnMonsterProjection(objectID int32, life content.Life) []byte {
	return spawnMonsterWithType(objectID, life, false, nil, time.Time{}, -4)
}

func spawnMonsterWithType(objectID int32, life content.Life, controller bool, stats []MobStatValue, now time.Time, summonType int8) []byte {
	op := SendSpawnMonster
	if controller {
		op = SendControlMonster
	}
	w := NewWriter(op)
	if controller {
		w.U8(1)
	}
	w.I32(objectID)
	w.U8(1)
	w.I32(int32(life.TemplateID))
	w.U8(0)
	WriteMobTempStat(w, stats, now)
	w.I16(int16(life.X))
	w.I16(int16(life.Y))
	w.U8(byte(4 + life.Facing))
	w.I16(0)
	w.I16(int16(life.Foothold))
	w.U8(byte(summonType))
	w.U8(0xff)
	w.I32(63000)
	// v148 CMob::Init (0x6089FD) reads one more i32 here. Templates with the
	// +540 flag also read four i32 next; no current content sets it.
	w.I32(0)
	if controller {
		w.I32(0)
		w.I32(0)
		w.U8(0xff)
	} else {
		w.U8(0)
	}
	if w.Err() != nil {
		return nil
	}
	return w.Packet()
}

func StopMonsterControl(objectID int32) []byte {
	w := NewWriter(SendControlMonster)
	w.U8(0)
	w.I32(objectID)
	return w.Packet()
}
func KillMonster(objectID int32) []byte {
	w := NewWriter(SendKillMonster)
	w.I32(objectID)
	w.U8(1)
	return w.Packet()
}

// RemoveMonster takes a mob out of the field with no death animation
// (Java MobPacket.killMonster animation 0), for a mob re-seated after its
// controller reported it below the field.
func RemoveMonster(objectID int32) []byte {
	w := NewWriter(SendKillMonster)
	w.I32(objectID)
	w.U8(0)
	return w.Packet()
}
func DamageMonster(objectID int32, damage int) []byte {
	return DamageMonsterLong(objectID, damage, nil)
}

// DamageMonsterLong also announces a total above the 32-bit range.
func DamageMonsterLong(objectID int32, damage int, long *LongDamageOut) []byte {
	w := NewWriter(SendDamageMonster)
	w.I32(objectID)
	w.U8(0)
	if damage < 0 {
		w.I32(int32(damage)) // A heal.
	} else {
		w.U32(long.Wire(damage))
	}
	return w.Packet()
}
func MonsterHP(objectID int32, hp, maxHP int64) []byte {
	w := NewWriter(SendMonsterHP)
	w.I32(objectID)
	p := int64(0)
	if maxHP > 0 {
		p = (hp*100 + maxHP - 1) / maxHP
	}
	w.U8(byte(max(0, min(100, p))))
	return w.Packet()
}

type MeleeTarget struct {
	ObjectID int32
	Damage   int
}
type MeleeAttack struct {
	Skill     int
	Targets   []MeleeTarget
	Direction byte
	Display   uint16
	Speed     byte
	Tick      uint32
	X, Y      int
	Wire      []byte // Detached original incoming body, excluding the opcode.
}

func ReadMeleeAttack(r *Reader) (MeleeAttack, error) {
	var a MeleeAttack
	start := r.Pos()
	r.U8()
	counts := r.U8()
	targets, hits := int(counts>>4), int(counts&15)
	a.Skill = int(r.I32())
	// These two first-warrior families use the normal, non-charge melee
	// layout. Other jobs' extra fields remain unsupported rather than guessed.
	maximumTargets := 1
	if a.Skill == 1001005 {
		maximumTargets = 6
	}
	if a.Skill != 0 && a.Skill != 1001004 && a.Skill != 1001005 || targets > maximumTargets || hits > 1 || targets > 0 && hits != 1 {
		return a, ErrMalformed
	}
	r.Skip(2)
	r.U16()
	r.U8()
	a.Direction = r.U8()
	a.Display = r.U16()
	r.Skip(4) // v148: i32, then the action byte.
	r.U8()
	a.Speed = r.U8()
	a.Tick = r.U32()
	r.U32()
	r.U32() // v148 adds an i32 before the targets.
	seen := make(map[int32]bool, targets)
	for i := 0; i < targets; i++ {
		t := MeleeTarget{ObjectID: r.I32()}
		r.Skip(18) // v148 target metadata (an i32 after the first three bytes).
		t.Damage = int(r.I32())
		r.Skip(8)
		if t.ObjectID <= 0 || t.Damage < 0 || seen[t.ObjectID] {
			return a, ErrMalformed
		}
		seen[t.ObjectID] = true
		a.Targets = append(a.Targets, t)
	}
	a.X = int(r.I16())
	a.Y = int(r.I16())
	if r.Err() != nil || r.Remaining() != 0 {
		return a, ErrMalformed
	}
	a.Wire = r.BytesBetween(start, r.Pos())
	return a, nil
}

func MeleeAttackPacket(c domain.Character, a MeleeAttack) []byte {
	w := NewWriter(SendMeleeAttack)
	w.I32(int32(c.ID))
	counts := byte(len(a.Targets) << 4)
	if len(a.Targets) > 0 {
		counts |= 1
	}
	w.U8(counts)
	w.U8(domain.WireLevelByte(c.Level))
	level := 0
	if a.Skill != 0 {
		if a.Skill != 1001004 && a.Skill != 1001005 {
			return nil
		}
		for _, skill := range c.Skills {
			if skill.ID == a.Skill {
				level = skill.Level
			}
		}
		if level < 1 || level > 20 {
			return nil
		}
	}
	w.U8(byte(level))
	if level > 0 {
		w.I32(int32(a.Skill))
	}
	w.U8(a.Direction)
	w.U16(a.Display)
	if a.Display&0x7fff < 0x19d {
		w.U8(a.Speed)
		w.U8(0)
		w.I32(0)
		for _, t := range a.Targets {
			w.I32(t.ObjectID)
			w.U8(7)
			w.I32(int32(t.Damage))
		}
		if a.Display&0x7fff == 0x11b {
			w.I16(int16(a.X))
			w.I16(int16(a.Y))
		}
	}
	return w.Packet()
}

type MonsterMove struct {
	ObjectID       int32
	MoveID         uint16
	UseSkill       bool
	Action         byte
	SkillData      uint32
	Pairs          [][2]int16
	Values         []int16
	StartX, StartY int16
	X, Y           int
	Fragments      []byte
}

func ReadMonsterMove(r *Reader) (MonsterMove, error) {
	var m MonsterMove
	m.ObjectID = r.I32()
	r.U8() // v148 CMob::SendMoveRequest (0x60C2D8) adds a byte after the object ID.
	m.MoveID = r.U16()
	m.UseSkill = r.U8() != 0
	m.Action = r.U8()
	m.SkillData = r.U32()
	pairs := int(r.U8())
	if pairs > 32 {
		return m, ErrMalformed
	}
	for i := 0; i < pairs; i++ {
		m.Pairs = append(m.Pairs, [2]int16{r.I16(), r.I16()})
	}
	values := int(r.U8())
	if values > 32 {
		return m, ErrMalformed
	}
	for i := 0; i < values; i++ {
		m.Values = append(m.Values, r.I16())
	}
	r.U8()
	extra := r.U32()
	r.Skip(12)
	if extra == 0x12 {
		_ = r.String()
	}
	r.U8()
	m.StartX = r.I16()
	m.StartY = r.I16()
	r.Skip(4)
	start := r.Pos()
	x, y, absolute := parseAbsolutePosition(r)
	m.Fragments = r.BytesBetween(start, r.Pos())
	m.X, m.Y = int(m.StartX), int(m.StartY)
	if absolute {
		m.X, m.Y = int(x), int(y)
	}
	n := int(r.U8())
	r.Skip((n + 1) / 2)
	r.Skip(8)
	r.Skip(21)
	r.Skip(6) // v148 tail: u8 flags, i32, u8.
	if r.Err() != nil || r.Remaining() != 0 || m.ObjectID <= 0 {
		return m, ErrMalformed
	}
	return m, nil
}

func MonsterMovePacket(m MonsterMove) []byte {
	w := NewWriter(SendMoveMonster)
	w.I32(m.ObjectID)
	w.Bool(m.UseSkill)
	w.U8(m.Action)
	w.U32(m.SkillData)
	w.U8(byte(len(m.Pairs)))
	for _, p := range m.Pairs {
		w.I16(p[0])
		w.I16(p[1])
	}
	w.U8(byte(len(m.Values)))
	for _, v := range m.Values {
		w.I16(v)
	}
	w.I16(m.StartX)
	w.I16(m.StartY)
	w.I16(8)
	w.I16(1)
	w.Bytes(m.Fragments)
	return w.Packet()
}
func MonsterMoveResponse(m MonsterMove, mp int) []byte {
	w := NewWriter(SendMoveMonsterResponse)
	w.I32(m.ObjectID)
	w.U16(m.MoveID)
	w.U8(0)
	w.I16(int16(mp))
	w.U8(0)
	w.U8(0)
	w.I32(0)
	return w.Packet()
}

// writeEXP writes an EXP amount in the active contract's width.
func writeEXP(w *Writer, value int64) {
	if domain.LongExperienceEnabled() {
		w.I64(value)
		return
	}
	w.I32(int32(value))
}

// writeLevel writes the character level as the launcher's i32 or, while the
// level32 patch is not active, the client's stock byte (saturated at 255).
func writeLevel(w *Writer, level int) {
	if domain.Level32Wire() {
		w.I32(int32(level))
		return
	}
	w.U8(byte(min(max(level, 0), 255)))
}

// writeStat writes a base stat or AP in the active contract's width.
func writeStat(w *Writer, value int) {
	if domain.LongStatsEnabled() {
		w.I32(int32(value))
		return
	}
	w.I16(int16(value))
}

func UpdateEXP(c domain.Character) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x10000)
	writeEXP(w, c.EXP)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

// UpdateCombatStats writes the level in the active level contract (see
// writeLevel). Values follow MapleStat's ascending bit order.
func UpdateCombatStats(c domain.Character) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x10 | 0x40 | 0x400 | 0x800 | 0x1000 | 0x2000 | 0x4000 | 0x8000 | 0x10000)
	writeLevel(w, c.Level)
	writeStat(w, c.STR)
	w.I32(int32(c.HP))
	w.I32(int32(c.WireMaxHP()))
	w.I32(int32(c.MP))
	w.I32(int32(c.WireMaxMP()))
	writeStat(w, c.AP)
	writeSP(w, c)
	writeEXP(w, c.EXP)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}
