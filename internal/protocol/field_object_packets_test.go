package protocol

import "testing"

func TestAffectedAreaAndTownPortalNativeLayouts(t *testing.T) {
	p := AffectedAreaCreated(AffectedArea{ObjectID: 3, Type: AreaPoison, OwnerID: 9, SkillID: 2111003, SkillLevel: 5, Box: Rect{-10, -20, 30, 40}, X: 1, Y: 2})
	r := NewReader(p)
	if r.U16() != SendAffectedAreaCreated || r.I32() != 3 || r.I32() != 1 || r.I32() != 9 || r.I32() != 2111003 || r.U8() != 5 || r.U16() != 0 || r.I32() != -10 || r.I32() != -20 || r.I32() != 30 || r.I32() != 40 || r.I32() != 0 || r.I16() != 1 || r.I16() != 2 || r.Remaining() != 0 {
		t.Fatalf("area %x", p)
	}
	if len(AffectedAreaRemoved(3, 2111003, true)) != 7 || len(AffectedAreaRemoved(3, 4221006, false)) != 6 {
		t.Fatal("only poison mist reads the eruption byte")
	}
	if p = TownPortalCreated(9, 5, 6, true); len(p) != 2+1+4+4 || p[2] != 0 {
		t.Fatalf("door create %x", p)
	}
	if len(TownPortalInfo(NoTownPortal, NoTownPortal, 0, 0, 0)) != 2+8 || len(TownPortalInfo(1000000, 50000, 2311002, 1, 2)) != 2+8+8 {
		t.Fatal("town portal info")
	}
}

func TestUseTownPortalNativeDirections(t *testing.T) {
	for mode := byte(0); mode <= 1; mode++ {
		body := []byte{0x2f, 0x13, 0, 0, mode}
		v, err := ReadUseTownPortal(NewReader(body))
		if err != nil || v.OwnerID != 4911 || v.ToTown != (mode == 0) {
			t.Fatalf("mode %d: %+v %v", mode, v, err)
		}
	}
	for _, body := range [][]byte{{0x2f, 0x13, 0, 0, 2}, {0x2f, 0x13, 0, 0}, {0x2f, 0x13, 0, 0, 1, 0}, {0, 0, 0, 0, 0}} {
		if _, err := ReadUseTownPortal(NewReader(body)); err == nil {
			t.Fatalf("accepted malformed use %x", body)
		}
	}
}
