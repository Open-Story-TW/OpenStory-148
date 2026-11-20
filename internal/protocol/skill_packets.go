package protocol

import (
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"time"
)

const (
	RecvSkillAllocate uint16 = 0xa2
	RecvSkillCast     uint16 = 0xa3
	RecvSkillCancel   uint16 = 0xa4
	// RecvItemCancel is sent by the item buff icon's right click (sub_919701,
	// Java CANCEL_ITEM_EFFECT) with the negative item ID; the skill icon uses 0x96.
	RecvItemCancel            uint16 = 0x7d
	RecvTakeDamage            uint16 = 0x5a
	RecvRegeneration          uint16 = 0x9f
	RecvPotentialRegeneration uint16 = 0xa0
)

const (
	FieldObstacleDamage = -3
	MobContactDamage    = -1
)

type SkillAllocation struct {
	Tick    uint32
	SkillID int
	// Count is how many points the request asks for. The native request has
	// none and means one; the launcher's quantity prompt appends it.
	Count int
}

// MaxSkillAllocation bounds the count of one request; no skill has as many
// levels.
const MaxSkillAllocation = 100

type SkillCast struct {
	Tick           uint32
	SkillID, Level int
	// Mass-spell casts (native emitter 0x89FF18, predicate 0x669D0E) carry
	// the caster position, a delay and a zero flag byte.
	HasPosition bool
	X, Y        int16
	ActionDelay uint16
	ItemID      int     // 4121006/4111009/5201008 projectile or item.
	Affected    byte    // Party-member byte of the native request.
	HasMobs     bool    // The request carried a monster list (possibly empty).
	Mobs        []int32 // Monster object IDs chosen by the client.
}
type PlayerDamage struct {
	Tick              uint32
	Type              int8
	Damage, MonsterID int
	ObjectID          int32
	Direction         byte
	// DefType is Java's defType byte: 1 is a Demon Slayer Power Guard
	// block (31110008).
	DefType byte
}
type Regeneration struct {
	Tick   uint32
	HP, MP int
	Opcode uint16
	Mode   byte
	Wire   []byte // Exact owned body; opcode is retained separately.
}

func ReadSkillAllocation(r *Reader) (SkillAllocation, error) {
	v := SkillAllocation{Tick: r.U32(), SkillID: int(r.I32()), Count: 1}
	if r.Remaining() == 4 {
		v.Count = int(r.I32())
	}
	if r.Err() != nil || r.Remaining() != 0 || v.SkillID < 1 || v.Count < 1 || v.Count > MaxSkillAllocation {
		return v, ErrMalformed
	}
	return v, nil
}
func ReadSkillCast(r *Reader) (SkillCast, error) {
	v := SkillCast{Tick: r.U32(), SkillID: int(r.I32()), Level: int(r.U8())}
	if r.Err() != nil || v.SkillID < 1 || v.Level < 1 {
		return v, ErrMalformed
	}
	tail := r.Bytes(r.Remaining())
	if parsed, ok := readSkillUseTail(v, tail); ok {
		return parsed, nil
	}
	// Pre-audit tolerance: short unparsed tails were accepted and ignored.
	if len(tail) > 7 {
		return v, ErrMalformed
	}
	return v, nil
}

// readSkillUseTail follows the native emitter 0x89FF18 after the level:
// position when castHasPosition, an item I32 for 4121006/4111009/5201008,
// an optional affected-member byte (2311001 adds delay16 + zero), an
// optional mob count + I32 object IDs, then delay16 and a zero byte.
func readSkillUseTail(v SkillCast, tail []byte) (SkillCast, bool) {
	r := NewReader(tail)
	if castHasPosition(v.SkillID) {
		v.X, v.Y, v.HasPosition = r.I16(), r.I16(), true
	}
	if v.SkillID == 4121006 || v.SkillID == 4111009 || v.SkillID == 5201008 {
		v.ItemID = int(r.I32())
	}
	middle := r.Remaining() - 3
	if r.Err() != nil || middle < 0 {
		return v, false
	}
	if middle > 0 {
		first := int(tail[len(tail)-r.Remaining()])
		if middle == 1+4*first {
			r.U8()
			v.HasMobs = true
		} else {
			v.Affected = r.U8()
			if v.SkillID == 2311001 {
				r.U16()
				if r.U8() != 0 {
					return v, false
				}
			}
			if r.Remaining() > 3 {
				first = int(r.U8())
				if r.Remaining() != 4*first+3 {
					return v, false
				}
				v.HasMobs = true
			}
		}
		if v.HasMobs {
			for r.Remaining() > 3 {
				v.Mobs = append(v.Mobs, r.I32())
			}
		}
	}
	v.ActionDelay = r.U16()
	if r.U8() != 0 || r.Err() != nil || r.Remaining() != 0 {
		return v, false
	}
	return v, true
}

// ReadItemCancel reads 0x73: the negative ID of a USE item whose buff icon
// was cancelled.
func ReadItemCancel(r *Reader) (int, error) {
	v := int(r.I32())
	if r.Err() != nil || r.Remaining() != 0 || v > -2000000 || v <= -3000000 {
		return 0, ErrMalformed
	}
	return v, nil
}

// ReadSkillCancel reads 0x96: the native emitter 0x89E37A always writes the
// skill I32 followed by a mode byte (0 from the buff icon, also sent when a
// mount skill is pressed again while riding). The legacy four-byte body is
// still accepted. A USE item buff is cancelled by its negative item ID.
func ReadSkillCancel(r *Reader) (int, error) {
	v := int(r.I32())
	if r.Remaining() == 1 {
		r.U8()
	}
	if r.Err() != nil || r.Remaining() != 0 || v == 0 || v < 0 && (v > -2000000 || v <= -3000000) {
		return 0, ErrMalformed
	}
	return v, nil
}
func ReadPlayerDamage(r *Reader) (PlayerDamage, error) {
	r.U32()
	v := PlayerDamage{Tick: r.U32(), Type: int8(r.U8())}
	element := r.U8()
	v.Damage = int(r.I32())
	if v.Type == FieldObstacleDamage {
		// Native lava/steam hits carry five zero bytes after the damage.
		if r.U32() != 0 || r.U8() != 0 || element != 0 || v.Damage <= 0 || r.Err() != nil || r.Remaining() != 0 {
			return v, ErrMalformed
		}
		return v, nil
	}
	r.Skip(2)
	if v.Type != -1 {
		return v, ErrMalformed
	}
	v.MonsterID = int(r.I32())
	v.ObjectID = r.I32()
	v.Direction = r.U8()
	skill := r.I32()
	reflect := r.I32()
	defense := r.U8()
	guard := r.U8()
	// Native OnHit8893F8 always appends this byte after the optional guard
	// extension. Only the ordinary, non-reflecting contact path is enabled.
	flags := r.U8()
	if r.Err() != nil || r.Remaining() != 0 || v.Damage < 0 || v.Damage > 200000 || v.MonsterID < 1 || v.ObjectID < 1 || v.Direction > 1 || skill != 0 || reflect != 0 || defense > 1 || guard != 0 || flags != 0 {
		return v, ErrMalformed
	}
	v.DefType = defense
	return v, nil
}

// Native93191B emits ordinary17B and93189E emits potential16B. Both
// include the exact 64-bit stat mask0x1400. No guessed legacy-length fallback.
func ReadRegeneration(r *Reader) (Regeneration, error) {
	return ReadRegenerationOpcode(RecvRegeneration, r)
}
func ReadRegenerationOpcode(op uint16, r *Reader) (Regeneration, error) {
	v := Regeneration{Opcode: op}
	expected := 17
	if op == RecvPotentialRegeneration {
		expected = 16
	} else if op != RecvRegeneration {
		return v, ErrMalformed
	}
	if r.Remaining() != expected {
		return v, ErrMalformed
	}
	v.Wire = append([]byte(nil), r.Bytes(expected)...)
	body := NewReader(v.Wire)
	v.Tick = body.U32()
	low, high := body.U32(), body.U32()
	v.HP, v.MP = int(body.I16()), int(body.I16())
	if op == RecvRegeneration {
		v.Mode = body.U8()
	}
	if body.Err() != nil || body.Remaining() != 0 || low != 0x1400 || high != 0 || v.HP < 0 || v.MP < 0 || v.HP == 0 && v.MP == 0 || v.Mode != 0 && v.Mode != 2 {
		return v, ErrMalformed
	}
	return v, nil
}

func AddSkillSections(w *Writer, c domain.Character, now time.Time) {
	w.U8(0)
	records := wireSkills(c)
	w.U16(uint16(len(records)))
	for _, s := range records {
		w.I32(int32(s.ID))
		w.I32(int32(s.Level))
	}
	w.U16(0)
	n := 0
	for _, s := range c.Skills {
		if !s.ExpiresAt.IsZero() {
			n++
		}
	}
	w.U16(uint16(n))
	for _, s := range c.Skills {
		if !s.ExpiresAt.IsZero() {
			w.I32(int32(s.ID))
			w.I64(s.ExpiresAt.UnixMilli())
		}
	}
	w.U16(0)
	n = 0
	for _, s := range c.Skills {
		if s.MasterLevel > 0 {
			n++
		}
	}
	w.U16(uint16(n))
	for _, s := range c.Skills {
		if s.MasterLevel > 0 {
			w.I32(int32(s.ID))
			w.I32(int32(s.MasterLevel))
		}
	}
	w.U16(0)
	n = 0
	for _, s := range c.SkillCooldowns {
		if s.ExpiresAt.After(now) {
			n++
		}
	}
	w.U16(uint16(n))
	for _, s := range c.SkillCooldowns {
		if s.ExpiresAt.After(now) {
			w.I32(int32(s.ID))
			// v148 CharacterData mask 0x8000 reads the remaining seconds as i32 (v145: u16).
			w.I32(int32(min(65535, max(1, int(s.ExpiresAt.Sub(now)/time.Second)))))
		}
	}
}
func SkillUpdate(skill domain.SkillState) []byte {
	w := NewWriter(0x31)
	w.U8(1)
	w.U8(0)
	w.U16(1)
	w.I32(int32(skill.ID))
	w.I32(int32(skill.Level))
	w.I32(int32(skill.MasterLevel))
	if skill.ExpiresAt.IsZero() {
		w.I64(maxTime)
	} else {
		w.I64(mapleTime(skill.ExpiresAt))
	}
	w.U8(4)
	return w.Packet()
}
func SkillCooldown(skillID, seconds int) []byte {
	w := NewWriter(0x19f)
	w.I32(int32(skillID))
	w.I32(int32(seconds))
	return w.Packet()
}
func SkillPointUpdate(sp int) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x8000)
	w.U16(uint16(sp))
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

// buffRows turns the rows of one skill effect into packet stats.
func buffRows(rows []domain.SkillBuff) ([]BuffStat, int, bool) {
	if len(rows) == 0 {
		return nil, 0, false
	}
	stats := make([]BuffStat, len(rows))
	for i, b := range rows {
		if b.ID != rows[0].ID {
			return nil, 0, false
		}
		stats[i] = BuffStat{Stat: buffstat.Stat(b.Stat), Value: b.Value}
	}
	return stats, rows[0].ID, true
}

// The summoned bird owns the effect lifetime. v148 BasicStat reads the
// learned Spirit Link skill from this triplet's source, independently of the
// summon ID used by the other rows and its cancellation icon.
func skillBuffWireSources(rows []domain.SkillBuff) map[int]int {
	for _, row := range rows {
		if linked := buffstat.SpiritLinkSkill(row.ID, row.Stat); linked != 0 {
			return map[int]int{169: linked}
		}
	}
	return nil
}

// SkillBuffs is the local temporary-stat packet for the rows of one skill.
func SkillBuffs(rows []domain.SkillBuff, now time.Time) []byte {
	stats, id, ok := buffRows(rows)
	if !ok {
		return nil
	}
	p, _ := temporaryStatSetIndie(id, rows[0].ExpiresAt.Sub(now), stats, nil, skillBuffWireSources(rows), rows[0].Level)
	return p
}

// SkillBuffsAmong is SkillBuffs whose indie stats list every active row of
// that stat in active (the owner's buffs), so stacked sources stay in the
// client's list. Rows of one source per stat give the same bytes as SkillBuffs.
func SkillBuffsAmong(rows, active []domain.SkillBuff, now time.Time) []byte {
	return skillBuffsAmong(rows, active, 0, now)
}

// SkillBuffsFor keeps the native Blue Aura owner marker on local updates.
func SkillBuffsFor(c domain.Character, rows []domain.SkillBuff, now time.Time) []byte {
	return skillBuffsAmong(rows, c.SkillBuffs, c.ID, now)
}

func skillBuffsAmong(rows, active []domain.SkillBuff, characterID int64, now time.Time) []byte {
	stats, id, ok := buffRows(rows)
	if !ok {
		return nil
	}
	for i, row := range rows {
		stats[i].blueAuraOwner = row.Stat == "BLUE_AURA" && characterID != 0 && row.SourceCharacterID == characterID
	}
	p, _ := temporaryStatSetIndie(id, rows[0].ExpiresAt.Sub(now), stats, IndieEntries(rows, active, now), skillBuffWireSources(rows), rows[0].Level)
	return p
}

// IndieEntries lists, for each indie stat of rows, the active rows of active
// holding it, with the source of rows first. Nil when no other source holds
// any of them.
func IndieEntries(rows, active []domain.SkillBuff, now time.Time) map[buffstat.Stat][]IndieEntry {
	var out map[buffstat.Stat][]IndieEntry
	for _, b := range rows {
		if !buffstat.Stackable(buffstat.Stat(b.Stat)) {
			continue
		}
		list := []IndieEntry{{Source: b.ID, Value: b.Value, Remaining: b.ExpiresAt.Sub(now)}}
		for _, o := range active {
			if o.Stat == b.Stat && o.ID != b.ID && o.ExpiresAt.After(now) {
				list = append(list, IndieEntry{Source: o.ID, Value: o.Value, Remaining: o.ExpiresAt.Sub(now)})
			}
		}
		if len(list) > 1 {
			if out == nil {
				out = map[buffstat.Stat][]IndieEntry{}
			}
			out[buffstat.Stat(b.Stat)] = list
		}
	}
	return out
}

func ForeignSkillBuffs(characterID int64, rows []domain.SkillBuff) []byte {
	stats, id, ok := buffRows(rows)
	if !ok {
		return nil
	}
	p, _ := foreignTemporaryStatSetFor(characterID, id, time.Until(rows[0].ExpiresAt), stats, skillBuffWireSources(rows))
	return p
}
func CancelSkillBuffs(characterID int64, rows []domain.SkillBuff) []byte {
	stats, _, ok := buffRows(rows)
	if !ok {
		return nil
	}
	names := make([]buffstat.Stat, len(stats))
	for i, s := range stats {
		names[i] = s.Stat
	}
	p, _ := TemporaryStatReset(characterID, names)
	return p
}

// SkillBuff, ForeignSkillBuff and CancelSkillBuff are the one-row forms.
func SkillBuff(b domain.SkillBuff, now time.Time) []byte {
	return SkillBuffs([]domain.SkillBuff{b}, now)
}
func ForeignSkillBuff(characterID int64, b domain.SkillBuff) []byte {
	return ForeignSkillBuffs(characterID, []domain.SkillBuff{b})
}
func CancelSkillBuff(characterID int64, b domain.SkillBuff) []byte {
	return CancelSkillBuffs(characterID, []domain.SkillBuff{b})
}

// GroupSkillBuffs splits rows into one group per skill, in first-seen order.
func GroupSkillBuffs(rows []domain.SkillBuff) [][]domain.SkillBuff {
	var out [][]domain.SkillBuff
	index := map[int]int{}
	for _, b := range rows {
		i, ok := index[b.ID]
		if !ok {
			i = len(out)
			index[b.ID] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], b)
	}
	return out
}

// effectExtraReads are the skills whose CUser::OnEffect0x85243A branch reads
// more than the base layout (Open-Maple docs/server/baseline/skill-effect-native.json).
// It also refuses an unknown skill or a level above the skill's maximum.
func effectExtraReads(kind byte, skillID int) bool {
	if def, e := content.ConfiguredSkillDefinition(skillID); e != nil || def.MaxLevel < 1 {
		return true
	}
	if kind == 2 {
		return skillID == 31111003
	}
	switch skillID {
	case 1320006, 22160000, 4331006, 30001061, 30001062, 80001132:
		return true
	}
	return skillID/10000000 == 9
}
func effectMaxLevel(skillID int) int {
	def, _ := content.ConfiguredSkillDefinition(skillID)
	return def.MaxLevel
}
func effectWriter(characterID int64, kind byte, skillID int) *Writer {
	op := uint16(0x154)
	if characterID != 0 {
		op = 0x144
	}
	w := NewWriter(op)
	if characterID != 0 {
		w.I32(int32(characterID))
	}
	w.U8(kind)
	w.I32(int32(skillID))
	return w
}

// SkillEffect is the type1 cast effect: skill, the caster's level (stored
// directly as the remote level, 0x852605) and the skill level.
func SkillEffect(characterID int64, skillID, characterLevel, skillLevel int) []byte {
	if characterID < 0 || characterID > 2147483647 || skillID < 1 || characterLevel < 1 || characterLevel > domain.MaxCharacterLevel() || skillLevel < 1 || skillLevel > effectMaxLevel(skillID) || effectExtraReads(1, skillID) {
		return nil
	}
	w := effectWriter(characterID, 1, skillID)
	w.U8(domain.WireLevelByte(characterLevel))
	w.U8(uint8(skillLevel))
	return w.Packet()
}

// SkillAffectedEffect is the type2 effect shown on a party-buff recipient.
func SkillAffectedEffect(characterID int64, skillID, skillLevel int) []byte {
	if characterID < 0 || characterID > 2147483647 || skillID < 1 || skillLevel < 1 || skillLevel > effectMaxLevel(skillID) || effectExtraReads(2, skillID) {
		return nil
	}
	w := effectWriter(characterID, 2, skillID)
	w.U8(uint8(skillLevel))
	return w.Packet()
}

// SkillDiceEffect follows native OnEffect case 3 at 0x8D7C75; its two
// I32 animation selectors precede the skill ID and skill rank.
func SkillDiceEffect(characterID int64, skillID, skillLevel int, roll domain.SkillDiceRoll) []byte {
	if characterID < 0 || characterID > 2147483647 || skillLevel < 1 || skillLevel > effectMaxLevel(skillID) ||
		roll.First < 1 || roll.First > 6 || roll.Second < 0 || roll.Second > 6 || (!roll.Upgraded && roll.Second != 0) {
		return nil
	}
	op := uint16(0x154)
	if characterID != 0 {
		op = 0x144
	}
	w := NewWriter(op)
	if characterID != 0 {
		w.I32(int32(characterID))
	}
	w.U8(3)
	w.I32(int32(roll.First))
	prefix := int32(-1)
	if roll.Upgraded && roll.Second == 0 {
		prefix = 0 // The upgraded skill has a separate affected0 branch.
	}
	w.I32(prefix)
	w.I32(int32(skillID))
	w.U8(uint8(skillLevel))
	w.U8(0)
	return w.Packet()
}
func PlayerDamagePacket(characterID int64, v PlayerDamage) []byte {
	w := NewWriter(0x13b)
	w.I32(int32(characterID))
	w.U8(uint8(v.Type))
	w.I32(int32(v.Damage))
	w.U8(0)
	w.I32(int32(v.MonsterID))
	w.U8(v.Direction)
	w.I32(0)
	w.I32(0)
	w.U8(0)
	w.U8(0)
	w.I32(int32(v.Damage))
	if v.Damage <= 0 {
		w.I32(0)
	}
	w.Zero(30)
	return w.Packet()
}
