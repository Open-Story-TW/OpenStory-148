package protocol

import "maple-go-server/internal/domain"

// Character info (角色情報): the main menu button and a double-click on a
// character both send RecvCharInfoRequest; the reply opens CUIUserInfo.
const (
	// v148 CWvsContext::SendCharacterInfoRequest (0x9CFDA9): update time i32,
	// character ID i32, then two flag bytes (pet/details).
	RecvCharInfoRequest uint16 = 0xa9
	// v148 CWvsContext::OnCharacterInfo (0x9E1F4B, OnPacket case 0x4C).
	SendCharInfo uint16 = 0x4c

	charInfoMedalSlot      = -46
	charInfoTamingMobSlot  = -18
	charInfoSaddleSlot     = -19
	charInfoPetEquipSlot   = -114
	charInfoTraitCount     = 6
	charInfoNoGuild        = "-"
	charInfoSelfPetUnknown = 0xff
)

// ReadCharInfoRequest returns the character the client asks about.
func ReadCharInfoRequest(r *Reader) (int64, error) {
	r.I32() // update time
	id := r.I32()
	r.U8()
	r.U8()
	if r.Err() != nil || id <= 0 {
		return 0, ErrMalformed
	}
	return int64(id), nil
}

func equippedItem(c domain.Character, slot int) int {
	for _, e := range c.Equipment {
		if e.Slot == slot {
			return e.ItemID
		}
	}
	return 0
}

// CharInfo is the reply for c. alliance is the guild's alliance name ("" for
// none); self is true when the requester asked about their own character.
//
// Layout read by 0x9E1F4B: id i32, level (i32 under the launcher's level32
// decode at 0x9E1FA0, else u8), job u16, sub job u16, PvP rank u8, fame i32,
// married u8, profession count u8 (+u16 each), guild str, alliance str,
// pet selector u8, tab u8, message str, expression/constellation/blood/
// month/day u8, flag u8; pets (0x89F934: u8 continue, index i32, item i32,
// name str, level u8, closeness u16, fullness u8, flags u16, pet equip i32);
// mount u8 (+level, exp, fatigue i32); wish list u8 (+i32 each); medal i32 +
// medal quest count u16 (0x89F7C4); six trait levels u8.
func CharInfo(c domain.Character, alliance string, self bool) []byte {
	w := NewWriter(SendCharInfo)
	w.I32(int32(c.ID))
	writeLevel(w, c.Level)
	w.U16(uint16(c.Job))
	w.U16(uint16(c.SubJob))
	w.U8(0) // PvP rank
	w.I32(int32(c.Fame))
	w.U8(0) // married
	w.U8(0) // professions
	if c.Guild.ID == 0 || c.Guild.Name == "" {
		w.String(charInfoNoGuild)
		w.String("")
	} else {
		w.String(c.Guild.Name)
		w.String(alliance)
	}
	if self {
		w.U8(charInfoSelfPetUnknown)
	} else {
		w.U8(0)
	}
	w.U8(0)      // tab
	w.String("") // character message
	w.Zero(5)    // expression, constellation, blood type, birth month, day
	w.U8(0)
	pets := [domain.MaxSummonedPets]*domain.Pet{}
	for i := range c.Pets {
		p := &c.Pets[i]
		if p.Summoned() && pets[p.Index()] == nil {
			pets[p.Index()] = p
		}
	}
	for i, p := range pets {
		if p == nil {
			continue
		}
		w.U8(1)
		w.I32(int32(i))
		w.I32(int32(p.TemplateID))
		w.String(p.Name)
		w.U8(byte(min(max(p.Level, 0), 255)))
		w.U16(uint16(p.Closeness))
		w.U8(byte(min(max(p.Fullness, 0), 255)))
		w.U16(uint16(p.Flags))
		if i == 0 {
			w.I32(int32(equippedItem(c, charInfoPetEquipSlot)))
		} else {
			w.I32(0)
		}
	}
	w.U8(0) // end of pets
	if equippedItem(c, charInfoTamingMobSlot) != 0 && equippedItem(c, charInfoSaddleSlot) != 0 {
		w.U8(1)
		w.I32(1) // mount level
		w.I32(0) // mount exp
		w.I32(0) // fatigue
	} else {
		w.U8(0)
	}
	w.U8(0) // wish list
	w.I32(int32(equippedItem(c, charInfoMedalSlot)))
	w.U16(0) // medal quests
	w.Zero(charInfoTraitCount)
	return w.Packet()
}
