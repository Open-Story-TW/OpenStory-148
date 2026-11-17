package protocol

import (
	"bytes"
	"testing"

	"maple-go-server/internal/domain"
)

func TestReadPetRequests(t *testing.T) {
	// [9C 00] tick slot lead, from Legacy PetHandler.SpawnPet.
	w := NewRawWriter()
	w.U32(7)
	w.U8(5)
	w.U8(1)
	spawn, err := ReadSpawnPet(NewReader(w.Packet()))
	if err != nil || spawn.Slot != 5 || !spawn.Lead || spawn.ClientTick != 7 {
		t.Fatalf("spawn %+v %v", spawn, err)
	}
	w = NewRawWriter()
	w.U32(1)
	w.I16(6)
	w.I32(2120000)
	food, err := ReadPetFood(NewReader(w.Packet()))
	if err != nil || food.Slot != 6 || food.TemplateID != 2120000 {
		t.Fatalf("food %+v %v", food, err)
	}
	w = NewRawWriter()
	w.U32(1)
	w.I16(6)
	w.I32(2000000)
	if _, err = ReadPetFood(NewReader(w.Packet())); err == nil {
		t.Fatal("non-food accepted")
	}
	w = NewRawWriter()
	w.I32(0)
	w.U8(0)
	w.U8(12)
	cmd, err := ReadPetCommand(NewReader(w.Packet()))
	if err != nil || cmd.Index != 0 || cmd.Command != 12 {
		t.Fatalf("command %+v %v", cmd, err)
	}
	w = NewRawWriter()
	w.I32(3)
	w.U8(0)
	w.U8(1)
	if _, err = ReadPetCommand(NewReader(w.Packet())); err == nil {
		t.Fatal("pet index 3 accepted")
	}
}

func TestReadPetMoveRelaysFragments(t *testing.T) {
	w := NewRawWriter()
	w.I32(1)
	w.U8(1)
	w.I16(10)
	w.I16(20)
	w.I32(0)
	frag := NewRawWriter()
	frag.U8(1)
	frag.U8(0)
	frag.I16(40)
	frag.I16(50)
	frag.Zero(4 + 2 + 4)
	frag.U8(4)
	frag.I16(30)
	w.Bytes(frag.Packet())
	w.Zero(9)
	move, err := ReadPetMove(NewReader(w.Packet()))
	if err != nil || move.Index != 1 || move.StartX != 10 || move.StartY != 20 || !move.Changed || move.X != 40 || move.Y != 50 || !bytes.Equal(move.Fragments, frag.Packet()) {
		t.Fatalf("move %+v %v", move, err)
	}
	out := NewReader(MovePet(77, move.Index, move.StartX, move.StartY, move.Fragments))
	if out.U16() != SendMovePet || out.I32() != 77 || out.I32() != 1 || out.I32() != 0 || out.I16() != 10 || out.I16() != 20 || !bytes.Equal(out.Bytes(out.Remaining()), frag.Packet()) {
		t.Fatal("move relay layout")
	}
}

func testPet() domain.Pet {
	return domain.Pet{CashID: 9001, TemplateID: 5000000, Slot: 3, Name: "Kitty", Level: 4, Closeness: 7, Fullness: 88, Summon: 2, X: -30, Y: 150, Stance: 4, Foothold: 12}
}

func TestSpawnAndRemovePetLayouts(t *testing.T) {
	p := testPet()
	r := NewReader(SpawnPet(55, p))
	if r.U16() != SendSpawnPet || r.I32() != 55 || r.I32() != 1 || r.U8() != 1 || r.U8() != 0 {
		t.Fatal("spawn header")
	}
	if r.I32() != 5000000 || r.String() != "Kitty" || r.I64() != 9001 || r.I16() != -30 || r.I16() != 150 || r.U8() != 4 || r.I16() != 12 || r.Remaining() != 0 || r.Err() != nil {
		t.Fatal("spawn pet info")
	}
	r = NewReader(RemovePet(55, 1, 2))
	if r.U16() != SendSpawnPet || r.I32() != 55 || r.I32() != 1 || r.U8() != 0 || r.U8() != 2 || r.Remaining() != 0 {
		t.Fatal("remove layout")
	}
	if RemovePet(55, 3, 0) != nil || SpawnPet(55, domain.Pet{}) != nil {
		t.Fatal("invalid pet packets encoded")
	}
}

func TestUpdatePetItemLayout(t *testing.T) {
	p := testPet()
	r := NewReader(UpdatePet(p))
	if r.U16() != SendModifyInventory || r.U8() != 0 || r.U8() != 2 || r.U8() != 0 || r.U8() != 3 || r.U8() != 5 || r.I16() != 3 || r.U8() != 0 || r.U8() != 5 || r.I16() != 3 {
		t.Fatal("modify header")
	}
	if r.U8() != 3 || r.I32() != 5000000 || r.U8() != 1 || r.I64() != 9001 || r.I64() != maxTime || r.I32() != -1 {
		t.Fatal("item base")
	}
	name := r.Bytes(13)
	if !bytes.Equal(name[:5], []byte("Kitty")) || !bytes.Equal(name[5:], make([]byte, 8)) {
		t.Fatal("fixed pet name")
	}
	if r.U8() != 4 || r.U16() != 7 || r.U8() != 88 || r.I64() != petPermanent || r.U16() != 0 || r.U16() != 0 || r.I32() != 0 || r.U16() != 0 || r.U8() != 2 || r.I32() != 0 || r.Remaining() != 0 {
		t.Fatal("pet body")
	}
}

func TestCashBagEncodesPetsAsTypeThree(t *testing.T) {
	p := testPet()
	item := domain.InventoryItem{CashID: p.CashID, TemplateID: p.TemplateID, InvType: 5, Slot: 3, Quantity: 1}
	w := NewRawWriter()
	addInventoryBags(w, []domain.InventoryItem{item}, p)
	r := NewReader(w.Packet())
	if r.U8() != 0 || r.U8() != 0 || r.U8() != 0 || r.U8() != 3 || r.U8() != 3 {
		t.Fatal("pet item missing from CASH bag")
	}
	plain := NewRawWriter()
	addInventoryBags(plain, []domain.InventoryItem{item})
	if bytes.Equal(plain.Packet(), w.Packet()) {
		t.Fatal("pet state ignored")
	}
}

func TestSpawnPlayerIncludesSummonedPets(t *testing.T) {
	c := domain.Character{ID: 5, Name: "Owner", Level: 10, Hair: 30000, Face: 20000}
	without := SpawnPlayer(c, 4)
	c.Pets = []domain.Pet{testPet(), {CashID: 1, TemplateID: 5000001, Name: "Rest"}}
	with := SpawnPlayer(c, 4)
	var info Writer
	info.U8(1)
	info.I32(1)
	addPetInfo(&info, testPet())
	if len(with) != len(without)+len(info.data) || !bytes.Contains(with, info.data) {
		t.Fatal("summoned pet not in SPAWN_PLAYER")
	}
}

func TestPetLevelForCloseness(t *testing.T) {
	for _, tc := range []struct{ closeness, level int }{{0, 1}, {1, 2}, {2, 2}, {3, 3}, {29999, 29}, {30000, 30}} {
		if got := domain.PetLevelFor(tc.closeness); got != tc.level {
			t.Fatalf("closeness %d level %d want %d", tc.closeness, got, tc.level)
		}
	}
}

func TestPetCommandResultMatchesV145Client(t *testing.T) {
	r := NewReader(PetCommandResult(5, 1, 7, true, false))
	if r.U16() != SendPetCommand || r.I32() != 5 || r.I32() != 1 || r.U8() != 0 || r.U8() != 7 || r.U8() != 1 || r.Remaining() != 0 {
		t.Fatal("interaction layout")
	}
	r = NewReader(PetCommandResult(5, 0, 1, false, true))
	if r.U16() != SendPetCommand || r.I32() != 5 || r.I32() != 0 || r.U8() != 1 || r.U8() != 0 || r.Remaining() != 0 {
		t.Fatal("food layout")
	}
}

func TestNoticeCarriesYellowChatFlag(t *testing.T) {
	r := NewReader(Notice("hi"))
	if r.U16() != 0x63 || r.U8() != 0xff || r.String() != "hi" || r.Remaining() != 0 {
		t.Fatal("yellow chat layout")
	}
}

func TestExpiredPetCarriesDeadMarker(t *testing.T) {
	p := testPet()
	p.ExpiresAt = 1
	var w Writer
	petExpiry(&w, p)
	if NewReader(w.data).I64() != maxTime {
		t.Fatal("expired pet must use the client dead marker")
	}
}
