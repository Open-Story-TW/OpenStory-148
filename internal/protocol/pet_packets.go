// Pet layouts derived from Legacy/OdinMS PetHandler, PetPacket and
// PacketHelper.GW_ItemSlotPet_RawDecode (AGPLv3).
// Original OdinMS authors: Patrick Huy, Matthias Butz, Jan Christian Meyer.
package protocol

import (
	"time"

	"maple-go-server/internal/domain"
)

const (
	RecvPetFood     uint16 = 0x80
	RecvSpawnPet    uint16 = 0xaa
	RecvMovePet     uint16 = 0x117
	RecvPetChat     uint16 = 0x118
	RecvPetCommand  uint16 = 0x119
	SendSpawnPet    uint16 = 0x121
	SendMovePet     uint16 = 0x122
	SendPetChat     uint16 = 0x123
	SendPetExcluded uint16 = 0x125
	SendShowPet     uint16 = 0x127
	SendPetCommand  uint16 = 0x128
	SendForeignFX   uint16 = 0x144
	SendOwnFX       uint16 = 0x154
)

type SpawnPetRequest struct {
	ClientTick uint32
	Slot       int
	Lead       bool
}

type PetFoodRequest struct {
	ClientTick uint32
	Slot       int
	TemplateID int
}

type PetMove struct {
	Index          int
	StartX, StartY int16
	X, Y           int
	Fragments      []byte
	Changed        bool
}

type PetChatRequest struct {
	Index int
	Act   int
	Text  string
}

type PetCommandRequest struct {
	Index   int
	Command int
}

func validPetIndex(i int32) bool { return i >= 0 && i < domain.MaxSummonedPets }

func ReadSpawnPet(r *Reader) (SpawnPetRequest, error) {
	v := SpawnPetRequest{ClientTick: r.U32(), Slot: int(r.U8()), Lead: r.U8() > 0}
	if r.Err() != nil || r.Remaining() != 0 || v.Slot < 1 || v.Slot > domain.MaxInventorySlots {
		return SpawnPetRequest{}, ErrMalformed
	}
	return v, nil
}

func ReadPetFood(r *Reader) (PetFoodRequest, error) {
	v := PetFoodRequest{ClientTick: r.U32(), Slot: int(r.I16()), TemplateID: int(r.I32())}
	if r.Err() != nil || r.Remaining() != 0 || v.Slot < 1 || v.Slot > domain.MaxInventorySlots || !domain.IsPetFood(v.TemplateID) {
		return PetFoodRequest{}, ErrMalformed
	}
	return v, nil
}

// ReadPetMove keeps the native movement list verbatim for relay; only the
// last absolute fragment updates the field-local position.
func ReadPetMove(r *Reader) (PetMove, error) {
	var v PetMove
	index := r.I32()
	v.StartX, v.StartY = r.I16(), r.I16()
	r.Skip(4)
	start := r.Pos()
	x, y, ok := parseAbsolutePosition(r)
	if r.Err() != nil || !validPetIndex(index) {
		return PetMove{}, ErrMalformed
	}
	v.Index = int(index)
	v.Fragments = r.BytesBetween(start, r.Pos())
	if len(v.Fragments) < 2 {
		return PetMove{}, ErrMalformed
	}
	if ok {
		v.X, v.Y, v.Changed = int(x), int(y), true
	}
	return v, nil
}

func ReadPetChat(r *Reader) (PetChatRequest, error) {
	index := r.I32()
	r.Skip(4)
	v := PetChatRequest{Act: int(r.U16()), Text: r.String()}
	if r.Err() != nil || r.Remaining() != 0 || !validPetIndex(index) || len(v.Text) < 1 || len(v.Text) > 70 {
		return PetChatRequest{}, ErrMalformed
	}
	v.Index = int(index)
	return v, nil
}

func ReadPetCommand(r *Reader) (PetCommandRequest, error) {
	index := r.I32()
	r.U8()
	v := PetCommandRequest{Command: int(r.U8())}
	if r.Err() != nil || r.Remaining() != 0 || !validPetIndex(index) {
		return PetCommandRequest{}, ErrMalformed
	}
	v.Index = int(index)
	return v, nil
}

func addPetInfo(w *Writer, p domain.Pet) {
	w.I32(int32(p.TemplateID))
	w.String(p.Name)
	w.I64(p.CashID)
	w.I16(int16(p.X))
	w.I16(int16(p.Y))
	w.U8(byte(p.Stance))
	w.I16(int16(p.Foothold))
}

// addForeignPets is SPAWN_PLAYER's summoned pet list and terminator.
func addForeignPets(w *Writer, c domain.Character) {
	for _, p := range c.SummonedPets() {
		w.U8(1)
		w.I32(int32(p.Index()))
		addPetInfo(w, p)
	}
	w.U8(0)
}

// petPermanent is Legacy PacketHelper.PERMANENT. The client (0x4C0451) treats
// a dateDead at or after maxTime as an expired "doll", so a live permanent pet
// must carry this slightly earlier value, and an expired one maxTime.
const petPermanent int64 = 150841440000000000

func petExpiry(w *Writer, p domain.Pet) {
	switch {
	case p.ExpiresAt <= 0:
		w.I64(petPermanent)
	case p.Expired(time.Now()):
		w.I64(maxTime)
	default:
		w.I64(mapleTime(time.UnixMilli(p.ExpiresAt)))
	}
}

// addPetBody is GW_ItemSlotPet after the GW_ItemSlotBase prefix.
func addPetBody(w *Writer, p domain.Pet) {
	w.FixedString(p.Name, 13)
	w.U8(byte(p.Level))
	w.U16(uint16(p.Closeness))
	w.U8(byte(p.Fullness))
	petExpiry(w, p)
	w.U16(0)
	w.U16(uint16(p.Flags))
	w.I32(0) // seconds left: only 5000054 uses it.
	w.U16(0)
	w.U8(byte(p.Summon))
	w.I32(int32(p.SkillID))
}

// addPetItem is a full GW_ItemSlotBase of type 3 for CASH inventory lists.
func addPetItem(w *Writer, p domain.Pet) {
	w.U8(3)
	w.I32(int32(p.TemplateID))
	addCashIdentity(w, p.CashID, p.ExpiresAt)
	w.I32(-1)
	addPetBody(w, p)
}

func petForItem(pets []domain.Pet, item domain.InventoryItem) (domain.Pet, bool) {
	if item.InvType != 5 || item.CashID <= 0 {
		return domain.Pet{}, false
	}
	for _, p := range pets {
		if p.CashID == item.CashID && p.TemplateID == item.TemplateID {
			return p, true
		}
	}
	return domain.Pet{}, false
}

func SpawnPet(characterID int64, p domain.Pet) []byte {
	if characterID <= 0 || !p.Summoned() {
		return nil
	}
	w := NewWriter(SendSpawnPet)
	w.I32(int32(characterID))
	w.I32(int32(p.Index()))
	w.U8(1)
	w.U8(0)
	addPetInfo(w, p)
	return w.Packet()
}

// ShowOwnPet is the field-entry form Legacy MapleMap.addPlayer sends to self.
func ShowOwnPet(characterID int64, p domain.Pet) []byte {
	if characterID <= 0 || !p.Summoned() {
		return nil
	}
	w := NewWriter(SendShowPet)
	w.I32(int32(characterID))
	w.I32(int32(p.Index()))
	w.U8(1)
	w.U8(0)
	addPetInfo(w, p)
	return w.Packet()
}

// RemovePet reasons: 0 recalled, 1 hunger, 2 expired.
func RemovePet(characterID int64, index, reason int) []byte {
	if characterID <= 0 || index < 0 || index >= domain.MaxSummonedPets || reason < 0 || reason > 2 {
		return nil
	}
	w := NewWriter(SendSpawnPet)
	w.I32(int32(characterID))
	w.I32(int32(index))
	w.U8(0)
	w.U8(byte(reason))
	return w.Packet()
}

func MovePet(characterID int64, index int, startX, startY int16, fragments []byte) []byte {
	if characterID <= 0 || index < 0 || index >= domain.MaxSummonedPets || len(fragments) < 2 {
		return nil
	}
	w := NewWriter(SendMovePet)
	w.I32(int32(characterID))
	w.I32(int32(index))
	w.I32(0)
	w.I16(startX)
	w.I16(startY)
	w.Bytes(fragments)
	return w.Packet()
}

func PetChat(characterID int64, index, act int, text string) []byte {
	if characterID <= 0 || index < 0 || index >= domain.MaxSummonedPets {
		return nil
	}
	w := NewWriter(SendPetChat)
	w.I32(int32(characterID))
	w.I32(int32(index))
	w.U16(uint16(act))
	w.String(text)
	return w.Packet()
}

// PetCommandResult follows the v145 client (CPet 0x62A182), not Legacy's
// later layout: type 0 is an interaction (command index, success) and type 1
// is food (success only). Legacy's food type 2 makes the client read garbage.
func PetCommandResult(characterID int64, index, command int, success, food bool) []byte {
	if characterID <= 0 || index < 0 || index >= domain.MaxSummonedPets {
		return nil
	}
	w := NewWriter(SendPetCommand)
	w.I32(int32(characterID))
	w.I32(int32(index))
	if food {
		w.U8(1)
	} else {
		w.U8(0)
		w.U8(byte(command))
	}
	w.Bool(success)
	return w.Packet()
}

func PetExcluded(characterID int64, p domain.Pet) []byte {
	if characterID <= 0 || !p.Summoned() {
		return nil
	}
	w := NewWriter(SendPetExcluded)
	w.I32(int32(characterID))
	w.I32(int32(p.Index()))
	w.I64(p.CashID)
	w.U8(0)
	return w.Packet()
}

// UpdatePet replaces the CASH slot item with fresh pet state (Legacy updatePet).
func UpdatePet(p domain.Pet) []byte {
	if p.CashID <= 0 || p.Slot < 1 || p.Slot > domain.MaxInventorySlots {
		return nil
	}
	w := NewWriter(SendModifyInventory)
	w.U8(0)
	w.U8(2)
	w.U8(0)
	w.U8(3)
	w.U8(5)
	w.I16(int16(p.Slot))
	w.U8(0)
	w.U8(5)
	w.I16(int16(p.Slot))
	addPetItem(w, p)
	return w.Packet()
}

func PetLevelUpSelf(index int) []byte {
	w := NewWriter(SendOwnFX)
	w.U8(6)
	w.U8(0)
	w.I32(int32(index))
	return w.Packet()
}

func PetLevelUpForeign(characterID int64, index int) []byte {
	w := NewWriter(SendForeignFX)
	w.I32(int32(characterID))
	w.U8(6)
	w.U8(0)
	w.I32(int32(index))
	return w.Packet()
}
