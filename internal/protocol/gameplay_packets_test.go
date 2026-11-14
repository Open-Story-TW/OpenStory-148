package protocol

import (
	"encoding/binary"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestMeleeParserRejectsTruncationAndSkillSpoofing(t *testing.T) {
	w := NewRawWriter()
	w.U8(0)
	w.U8(0x11)
	w.I32(0)
	w.Zero(2)
	w.U16(0)
	w.U8(0)
	w.U8(1)
	w.U16(35)
	w.Zero(2)
	w.Zero(3)
	w.U8(6)
	w.U32(100)
	w.U32(0)
	w.U32(0) // v148 melee i32 before the targets
	w.I32(1000001)
	w.Zero(18)
	w.I32(8)
	w.Zero(8)
	w.I16(500)
	w.I16(155)
	p := w.Packet()
	a, err := ReadMeleeAttack(NewReader(p))
	if err != nil {
		t.Fatal(err)
	}
	if a.Tick != 100 || len(a.Targets) != 1 || a.Targets[0].ObjectID != 1000001 || a.Targets[0].Damage != 8 {
		t.Fatalf("parsed attack=%+v", a)
	}
	for n := 0; n < len(p); n++ {
		if _, err := ReadMeleeAttack(NewReader(p[:n])); err == nil {
			t.Fatalf("accepted %d-byte truncated attack", n)
		}
	}
	p[2] = 1
	if _, err := ReadMeleeAttack(NewReader(p)); err == nil {
		t.Fatal("accepted unsupported skill")
	}
}

func TestGameplayPacketFixedFields(t *testing.T) {
	life := content.Life{TemplateID: 9300018, X: 505, Y: 155, Foothold: 19, RX0: -250, RX1: 1150}
	p := SpawnMonster(1000001, life, false)
	if len(p) != 64 || binary.LittleEndian.Uint16(p) != SendSpawnMonster || binary.LittleEndian.Uint32(p[7:11]) != 9300018 {
		t.Fatalf("mob spawn bytes=%x len=%d", p, len(p))
	}
	if p[11] != 0 {
		t.Fatal("unexpected changed mob stats")
	}
	for _, v := range p[12:44] {
		if v != 0 {
			t.Fatal("unexpected mob buff mask")
		}
	}
	ctrl := SpawnMonster(1000001, life, true)
	if ctrl[2] != 1 || binary.LittleEndian.Uint32(ctrl[3:7]) != 1000001 {
		t.Fatalf("controller=%x", ctrl)
	}
	warp := WarpMap(domain.Character{MapID: 20000, SpawnPoint: 1, HP: 50}, 2)
	if binary.LittleEndian.Uint32(warp[2:6]) != 1 || warp[11] != 2 || binary.LittleEndian.Uint32(warp[20:24]) != 20000 {
		t.Fatalf("warp=%x", warp)
	}
	npc := SpawnNPC(101, content.Life{TemplateID: 2100, X: 833, Y: 125, Foothold: 9, RX0: 783, RX1: 883}, false)
	if len(npc) != 22 || binary.LittleEndian.Uint32(npc[6:10]) != 2100 {
		t.Fatalf("npc=%x", npc)
	}
}

// WarpMapAt sets the SetField chase flag and position after HP (0x69EDC2).
func TestWarpMapAtChasePosition(t *testing.T) {
	c := domain.Character{MapID: 104000000, SpawnPoint: 20, HP: 50}
	plain, at := WarpMap(c, 1), WarpMapAt(c, 1, 1081, 647)
	// opcode2 + ch4 + 1+4+1+4+1+2+1 + map4 + portal1 + hp4 = 29, then the flag.
	if len(at) != len(plain)+8 || plain[29] != 0 || at[29] != 1 || binary.LittleEndian.Uint32(at[30:]) != 1081 || binary.LittleEndian.Uint32(at[34:]) != 647 {
		t.Fatalf("chase layout\n% x\n% x", plain, at)
	}
}
