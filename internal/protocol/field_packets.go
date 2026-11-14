// Field spawn/move/remove layouts derived from Legacy/OdinMS CField (AGPLv3).
// Original OdinMS authors: Patrick Huy, Matthias Butz, Jan Christian Meyer.
package protocol

import "maple-go-server/internal/domain"

const (
	SendSpawnPlayer  uint16 = 0x100
	SendRemovePlayer uint16 = 0x101
	SendMovePlayer   uint16 = 0x132
)

// SpawnPlayer writes a no-buff foreign player spawn with guild mark and summoned pets.
// Buff mask matches Legacy default foreign buffs 248..255 on MAX_BUFFSTAT=8.
func SpawnPlayer(c domain.Character, stance byte) []byte {
	if c.ID <= 0 || c.Level < 1 || c.Level > domain.MaxWireLevel() {
		return nil
	}
	w := NewWriter(SendSpawnPlayer)
	w.I32(int32(c.ID))
	w.U8(domain.WireLevelByte(c.Level))
	w.String(c.Name)
	w.String("") // ultimate explorer tag
	addGuildMark(w, c.Guild)
	writeEmptyForeignBuffs(w)
	w.U16(uint16(c.Job))
	w.U16(uint16(c.SubJob))
	addCharLook(w, c)
	w.I32(0)
	w.I32(0)
	w.I32(0)
	w.I32(0)
	w.I32(0) // v1/v2/v3 loop empty
	w.I32(0) // heart count
	w.I32(0) // item effect
	w.I32(0)
	w.I32(0)
	w.I32(0)
	w.I32(0) // nick/title ints
	w.String("")
	w.String("")
	w.U16(0xFFFF)
	w.U16(0xFFFF)
	w.U8(0)
	w.I32(0) // chair
	w.I32(0)
	w.I16(int16(c.X))
	w.I16(int16(c.Y))
	w.U8(stance)
	w.U16(0) // foothold
	addForeignPets(w, c)
	w.I32(1) // mount level
	w.I32(0) // mount exp
	w.I32(0) // fatigue
	w.U8(0)  // announce box
	w.Bool(false)
	w.U8(0)  // ring set A
	w.U8(0)  // ring set B
	w.U8(0)  // marriage rings
	w.U8(0)  // v65
	w.U8(0)  // berserk/unk
	w.I32(0) // mount item
	w.Zero(4)
	return w.Packet()
}

func writeEmptyForeignBuffs(w *Writer) {
	// Positions for buffs 248..255 all land in mask[0] as 0xFF000000.
	w.U32(0xFF000000)
	for i := 0; i < 7; i++ {
		w.I32(0)
	}
	// Legacy addForeignBuffState writes three unconditional flags between the
	// 256-bit mask/optional stats and the eight default temporary-stat records.
	// Omitting them shifts the foreign appearance and makes native CUserRemote
	// run past the end of SPAWN_PLAYER when a second player enters the map.
	w.Zero(3)
	const magic int32 = 1
	// ENERGY_CHARGE, DASH_SPEED, DASH_JUMP
	for i := 0; i < 3; i++ {
		w.I32(0)
		w.I32(0)
		w.U8(1)
		w.I32(magic)
		w.U16(0)
	}
	// MONSTER_RIDING (no source): flychair + skill
	w.I32(0)
	w.I32(0)
	w.U8(1)
	w.I32(magic)
	// SPEED_INFUSION
	w.I32(0)
	w.I32(0)
	w.U8(1)
	w.I32(magic)
	w.U8(1)
	w.I32(0)
	w.U16(0)
	// HOMING_BEACON
	w.I32(0)
	w.I32(0)
	w.U8(1)
	w.I32(magic)
	w.I32(0)
	w.I32(0)
	// DEFAULTBUFF1
	w.I32(0)
	w.I32(0)
	w.U8(1)
	w.I32(magic)
	w.U16(0)
	// DEFAULTBUFF2
	w.I32(0)
	w.I32(0)
	w.U8(1)
	w.I32(magic)
}

func RemovePlayer(characterID int64) []byte {
	if characterID <= 0 {
		return nil
	}
	w := NewWriter(SendRemovePlayer)
	w.I32(int32(characterID))
	return w.Packet()
}

// MovePlayer relays an already-parsed movement fragment list to peers.
func MovePlayer(characterID int64, startX, startY int16, fragments []byte) []byte {
	if characterID <= 0 || len(fragments) < 1 {
		return nil
	}
	w := NewWriter(SendMovePlayer)
	w.I32(int32(characterID))
	w.I16(startX)
	w.I16(startY)
	w.I32(0)
	w.Bytes(fragments)
	return w.Packet()
}
