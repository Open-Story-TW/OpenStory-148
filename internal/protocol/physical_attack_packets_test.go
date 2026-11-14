package protocol

import (
	"maple-go-server/internal/domain"
	"testing"
)

func physicalMeleeBody(skill int, ids ...int32) []byte {
	w := NewRawWriter()
	w.U8(0)
	w.U8(byte(len(ids)<<4) | 1)
	w.I32(int32(skill))
	w.Zero(2)
	w.U16(0)
	w.U8(0)
	w.U8(1)
	w.U16(35)
	w.Zero(2)
	w.Zero(3)
	w.U8(6)
	w.U32(99)
	w.U32(0)
	w.U32(0) // v148 melee i32 before the targets
	for _, id := range ids {
		w.I32(id)
		w.Zero(18)
		w.I32(12)
		w.Zero(8)
	}
	w.I16(10)
	w.I16(20)
	return w.Packet()
}

func TestPhysicalMeleeKnownFamiliesAndCompleteTargetValidation(t *testing.T) {
	body := physicalMeleeBody(1001005, 1, 2, 3, 4, 5, 6)
	a, err := ReadMeleeAttack(NewReader(body))
	if err != nil || len(a.Targets) != 6 || a.Skill != 1001005 {
		t.Fatalf("slash-blast parser %+v %v", a, err)
	}
	for n := 0; n < len(body); n++ {
		if _, err = ReadMeleeAttack(NewReader(body[:n])); err == nil {
			t.Fatalf("partial attack accepted length%d", n)
		}
	}
	for _, bad := range [][]byte{physicalMeleeBody(1001004, 1, 2), physicalMeleeBody(1001005, 1, 1), physicalMeleeBody(1001005, 1, 2, 3, 4, 5, 6, 7), physicalMeleeBody(1100002, 1)} {
		if _, err = ReadMeleeAttack(NewReader(bad)); err == nil {
			t.Fatal("unsupported/duplicate/overcount melee accepted")
		}
	}
	c := domain.Character{ID: 7, Level: 10, Skills: []domain.SkillState{{ID: 1001005, Level: 3}}}
	r := NewReader(MeleeAttackPacket(c, a))
	if r.U16() != SendMeleeAttack || r.I32() != 7 || r.U8() != 0x61 || r.U8() != 10 || r.U8() != 3 || r.I32() != 1001005 {
		t.Fatal("foreign physical skill identity/level was not server-derived")
	}
}
