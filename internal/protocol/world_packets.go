// Packet layouts derived from the Legacy/OdinMS Java source, licensed AGPLv3.
// Original OdinMS authors: Patrick Huy, Matthias Butz, Jan Christian Meyer.
package protocol

import (
	"crypto/rand"
	"encoding/binary"
	"sort"
	"time"

	"maple-go-server/internal/domain"
)

// SetField writes the Legacy full character mask and supported inventory,
// quest and skill sections. Unimplemented pet/event sections remain empty.
func SetField(c domain.Character, channel int) []byte {
	var seeds [12]byte
	if _, err := rand.Read(seeds[:]); err != nil {
		return nil
	}
	return setFieldAt(c, channel, time.Now(), seeds)
}
func setFieldAt(c domain.Character, channel int, now time.Time, seeds [12]byte) []byte {
	if !validCharacter(c) || channel < 1 || channel > 20 {
		return nil
	}
	w := NewWriter(SendSetField)
	w.I32(int32(channel - 1))
	w.U8(0)
	w.I32(0)
	w.U8(1)
	w.I32(0)
	w.U8(1)
	w.U16(0)
	for i := 0; i < 3; i++ {
		value := binary.LittleEndian.Uint32(seeds[i*4:])
		value |= []uint32{0x100000, 0x1000, 0x10}[i]
		w.U32(value)
	}
	addCharacterInfo(w, c, now)
	w.Zero(16)
	w.I64(mapleTime(now))
	w.I32(100)
	w.U8(0)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

func addCharacterInfo(w *Writer, c domain.Character, now time.Time) {
	// v148 CharacterData::Decode (0x4D5A4A) gained sections behind high mask
	// bits 2 (84-byte block) and 4 (four i32 + 32 bytes); only bit 1 is sent.
	w.I64(0x1_ffffffff)
	w.U8(0)
	w.U8(0)
	w.I32(0)
	w.U8(0) // mask, puzzle, expiration counts.
	addCharStats(w, c, now)
	// The friend list size; the world sends the character's own right after.
	buddyCount(w, domain.EffectiveBuddyCapacity(domain.DefaultBuddyCapacity))
	w.Zero(3)
	// CharacterData+2026 (0x4D5C5C). The cash shop button (0x9AFED9) asks
	// the 7-day purchase agreement (string 7854) while now minus this is
	// under seven days, so an old time keeps the button direct.
	w.I64(zeroTime)
	writeMoney(w, c.Mesos)
	w.I32(int32(c.ID))
	w.I32(0)
	w.I32(0)
	w.I32(0) // mask 8/0x2000000 extra stats.
	for invType := 1; invType <= 5; invType++ {
		w.U8(byte(c.Capacity(invType)))
	}
	w.I64(zeroTime) // capacities, pendant expiry.
	equipped := append([]domain.Equipment(nil), c.Equipment...)
	sort.Slice(equipped, func(i, j int) bool { return equipped[i].Slot > equipped[j].Slot })
	for _, e := range equipped {
		position := -e.Slot
		if position > 0 && position < 100 {
			w.U16(uint16(position))
			addEquipment(w, e)
		}
	}
	w.U16(0) // equipped normal terminator.
	for _, e := range equipped {
		position := -e.Slot
		if position > 100 && position < 200 {
			w.U16(uint16(position - 100))
			addEquipment(w, e)
		}
	}
	w.U16(0) // equipped cash terminator.
	AddEquipmentInventory(w, c.Equipment)
	w.Zero(8) // four special equipment category terminators.
	addInventoryBags(w, c.Items, c.Pets...)
	w.I32(-1) // extended slots terminator.
	w.I32(0)
	w.I32(0)
	w.U8(0) // masks 0x1000000,0x40000000,0x800000.
	AddSkillSections(w, c, now)
	AddQuestSections(w, c.Quests, c.QuestRecords)
	w.U16(0)  // minigame.
	w.Zero(6) // three ring lists.
	for i := 0; i < 28; i++ {
		w.I32(999999999)
	} // regular/VIP/hyper rocks.
	w.U16(0) // questinfo.
	if c.Job/100 == 33 {
		// mask 0x200000 GW_WildHunterInfo (0x4BB979): the jaguar (Legacy quest
		// 111112). Without it the client refuses Jaguar Rider 33001001
		// ("capture a jaguar first"); 10 is the first jaguar, mount 1932015.
		w.U8(domain.DefaultJaguar)
		w.Zero(5 * 4)
	}
	w.U16(0)       // mask0x400000 unknown list.
	w.U16(0)       // NPC limited-purchase list (mask 0x4000000).
	w.Zero(13 * 4) // mask 0x20000000 phantom stolen slots; v148 groups are 4+4+3+2.
	w.Zero(4 * 4)  // mask 0x10000000 phantom chosen skills 24001001/24101001/24111001/24121001.
	w.U16(0)       // mask 0x80000000 v148 {u8,i32,u8,u8} list.
	w.I32(0)       // high mask 1: two i32.
	w.I32(0)
	w.U16(0) // mask 0x80000000 OX system.
	w.U16(0) // high mask 1: exp chair list.
}

// addInventoryBags writes USE/SETUP/ETC/CASH lists (Legacy PacketHelper other=2..5).
// Each list is slot(byte)+GW_ItemSlotBundle, terminated by slot 0.
// CASH serials with pet state are GW_ItemSlotPet (type 3), not bundles.
func addInventoryBags(w *Writer, items []domain.InventoryItem, pets ...domain.Pet) {
	for invType := 2; invType <= 5; invType++ {
		for _, item := range items {
			if item.InvType != invType || item.Slot < 1 || item.Slot > 127 || item.Quantity < 1 || item.TemplateID <= 0 {
				continue
			}
			w.U8(byte(item.Slot))
			if pet, ok := petForItem(pets, item); ok {
				addPetItem(w, pet)
				continue
			}
			addBundleItem(w, item)
		}
		w.U8(0)
	}
}

func addBundleItem(w *Writer, item domain.InventoryItem) {
	w.U8(2) // Item.getType() for non-equip bundles
	w.I32(int32(item.TemplateID))
	addCashIdentity(w, item.CashID, item.ExpiresAt)
	w.I32(-1)
	w.U16(uint16(item.Quantity))
	w.String("")
	w.U16(0)
	// GW_ItemSlotBundle (0x4C3A6C): rechargeable stars (207), bullets (233)
	// and 302 chairs carry an 8-byte serial; Legacy writes -1 when unset.
	if kind := item.TemplateID / 10000; kind == 207 || kind == 233 || kind == 302 {
		w.I64(-1)
	}
}

func addEquipment(w *Writer, e domain.Equipment) {
	w.U8(1)
	w.I32(int32(e.ItemID))
	addCashIdentity(w, e.CashID, e.ExpiresAt)
	w.I32(-1)
	w.U8(byte(e.UpgradeSlots))
	w.U8(byte(e.ScrollLevel))
	for _, v := range []int{e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP, e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, 0, e.Speed, e.Jump} {
		w.U16(uint16(v))
	}
	w.String("")
	w.I32(int32(e.Flags))
	w.U8(0)
	w.U8(1)
	w.I32(0)
	w.I32(-1)
	w.I32(0)
	w.U16(0)
	w.U8(e.Potential.RawState)
	w.U8(e.Enhance)
	if e.Potential.IsZero() {
		// Preserve the no-potential TMS145 record's 0xffff sentinels in the
		// last two option fields. Enhance is independent of potential.
		w.Zero(6)
		w.I16(-1)
		w.I16(-1)
		w.U16(0)
	} else {
		// Native GW_ItemSlotEquip::Decode/Encode has five U16 option fields
		// and a separate U16 flags field.
		for _, option := range e.Potential.Options {
			w.U16(option)
		}
		w.U16(equipSocketState(e.Potential.Flags))
	}
	// v148 GW_ItemSlotEquip::RawDecode (0x4D1F1C) reads three more u16
	// after the flags (stored at +64/+72/+80 of the option block).
	w.Zero(3 * 2)
	// GW_ItemSlotEquip::Decode (0x4C3979) reads a separate unique ID only
	// without a cash serial. Cash equipment already carries that identity in
	// addCashIdentity; an extra eight bytes corrupt the next locker record.
	if e.CashID == 0 {
		w.I64(-1)
	}
	w.I64(zeroTime)
	w.I32(-1)
}

// equipSocketState is the u16 after the five option fields. v148 reads it as
// the socket state (item +339): 0xffff, the TMS145 placeholder, makes the
// client draw a socket icon it has no image for, and it exits with
// "error code : -2147467261" as soon as the inventory window opens.
func equipSocketState(flags uint16) uint16 {
	if flags == 0xffff {
		return 0
	}
	return flags
}

func EnableActions() []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0)
	w.U8(0)
	w.U8(0)
	return w.Packet()
}
func EnableCashshop() []byte      { w := NewWriter(0xa); w.U8(1); w.I32(0); return w.Packet() }
func ResetTemporaryStats() []byte { return Simple(0x30) }
func DefaultKeymap() []byte       { return KeymapPacket(domain.DefaultKeymap()) }
func ServerBlocked(reason byte) []byte {
	w := NewWriter(SendServerBlocked)
	w.U8(reason)
	return w.Packet()
}

// Notice is YELLOW_CHAT (0x5D). The v145 handler (0x93E3C7) reads a flag
// byte before the string; without it the string length is misread.
func Notice(message string) []byte {
	w := NewWriter(0x63)
	w.U8(0xff)
	w.String(message)
	return w.Packet()
}
