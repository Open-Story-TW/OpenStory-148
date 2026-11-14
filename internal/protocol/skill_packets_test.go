package protocol

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestNativeSkillPackets(t *testing.T) {
	w := NewRawWriter()
	w.U32(2)
	w.I32(1002)
	w.U8(1)
	q, e := ReadSkillCast(NewReader(w.Packet()))
	if e != nil || q.SkillID != 1002 || q.Level != 1 {
		t.Fatalf("cast%+v %v", q, e)
	}
	w = NewRawWriter()
	w.U32(1)
	w.U32(2)
	w.U8(255)
	w.U8(0)
	w.I32(5)
	w.U16(0)
	w.I32(100000)
	w.I32(1000001)
	w.U8(1)
	w.I32(0)
	w.I32(0)
	w.U8(0)
	w.U8(0)
	w.U8(0)
	d, e := ReadPlayerDamage(NewReader(w.Packet()))
	if e != nil || d.Damage != 5 || d.ObjectID != 1000001 {
		t.Fatalf("damage%+v %v", d, e)
	}
	for _, n := range []int{0, 7, 9, 11, 13, 15, 17} {
		if _, e := ReadRegeneration(NewReader(make([]byte, n))); e == nil {
			t.Fatalf("regen accepted%dbytes", n)
		}
	}
}

func TestReviewedBuffEffectNativeExactLevelsAndLengths(t *testing.T) {
	defer domain.SetIntLevelCapForTest(0)() // stock level cap
	for _, skill := range []int{1001, 1002, 1001003} {
		for _, id := range []int64{0, 42} {
			packet := SkillEffect(id, skill, 10, 1)
			wantLength, wantOpcode := 9, uint16(0x154)
			if id != 0 {
				wantLength, wantOpcode = 13, 0x144
			}
			if len(packet) != wantLength {
				t.Fatalf("skill%d actor%d effect length%d", skill, id, len(packet))
			}
			r := NewReader(packet)
			if r.U16() != wantOpcode {
				t.Fatal("effect route")
			}
			if id != 0 && r.I32() != int32(id) {
				t.Fatal("foreign effect actor")
			}
			if r.U8() != 1 || r.I32() != int32(skill) || r.U8() != 10 || r.U8() != 1 || r.Remaining() != 0 {
				t.Fatalf("native type1 effect fields shifted: %x", packet)
			}
		}
	}
	for _, q := range []struct{ skill, level, slv int }{{999, 10, 1}, {30001062, 10, 1}, {1001, 0, 1}, {1001, 201, 1}, {1001, 10, 0}, {1001, 10, 4}, {1001003, 10, 21}} {
		if SkillEffect(1, q.skill, q.level, q.slv) != nil {
			t.Fatalf("unreviewed effect shape accepted: %+v", q)
		}
	}
}

func TestIronBodyNativeLocalAndForeignPacketBoundaries(t *testing.T) {
	now := time.Now()
	b := domain.SkillBuff{ID: 1001003, Level: 1, Stat: "WDEF", Value: 10, ExpiresAt: now.Add(110 * time.Second)}
	p := SkillBuff(b, now)
	if len(p) != 50 || p[30] != 2 {
		t.Fatalf("local WDEF packet boundary %x", p)
	}
	r := NewReader(p[34:])
	if r.I16() != 10 || r.I32() != 1001003 || r.I32() != 110000 {
		t.Fatal("local WDEF source/value/duration widths wrong")
	}
	p = ForeignSkillBuff(9, b)
	if len(p) != 43 || p[34] != 2 {
		t.Fatalf("foreign WDEF must have no value payload %x", p)
	}
}
func TestSkillMasksAndSnapshot(t *testing.T) {
	unknown := domain.SkillBuff{Stat: "unknown"}
	if SkillBuff(unknown, time.Now()) != nil || ForeignSkillBuff(1, unknown) != nil || CancelSkillBuff(1, unknown) != nil {
		t.Fatal("unknown buff stat emitted a packet")
	}
	now := time.Unix(1800000000, 0)
	b := domain.SkillBuff{ID: 1002, Level: 1, Stat: "SPEED", Value: 10, ExpiresAt: now.Add(4 * time.Second)}
	r := NewReader(SkillBuff(b, now))
	if r.U16() != 0x2d {
		t.Fatal("opcode")
	}
	for i := 0; i < 7; i++ {
		if r.U32() != 0 {
			t.Fatal("mask position")
		}
	}
	if r.U32() != 128 || r.I16() != 10 || r.I32() != 1002 || r.I32() != 4000 {
		t.Fatal("buffdata")
	}
	if r.U8() != 0 || r.U8() != 0 || r.U8() != 0 || r.U16() != 0 || r.U8() != 0 || r.U8() != 0 || r.Err() != nil || r.Remaining() != 0 {
		t.Fatal("local native buff tail")
	}
	r = NewReader(ForeignSkillBuff(42, b))
	if r.U16() != 0x145 || r.I32() != 42 {
		t.Fatal("foreign buff header")
	}
	r.Skip(32)
	if r.U8() != 10 || r.U8() != 0 || r.U8() != 0 || r.U8() != 0 || r.U16() != 0 || r.Remaining() != 0 {
		t.Fatal("foreign speed must be U8, then native tail")
	}
	b.Stat = "RECOVERY"
	r = NewReader(ForeignSkillBuff(42, b))
	r.Skip(2 + 4 + 32)
	if r.U8() != 0 || r.U8() != 0 || r.U8() != 0 || r.U16() != 0 || r.Remaining() != 0 {
		t.Fatal("foreign recovery must have no value payload")
	}
	r = NewReader(CancelSkillBuff(42, b))
	if r.U16() != 0x146 || r.I32() != 42 {
		t.Fatal("foreign cancel")
	}
	for i := 0; i < 6; i++ {
		if r.U32() != 0 {
			t.Fatal("recovery mask")
		}
	}
	if r.U32() != 4 || r.U32() != 0 {
		t.Fatal("recovery mask bit")
	}
	w := NewRawWriter()
	AddSkillSections(w, domain.Character{Skills: []domain.SkillState{{ID: 1002, Level: 1}}, SkillCooldowns: []domain.SkillCooldown{{ID: 1002, ExpiresAt: now.Add(60 * time.Second)}}}, now)
	r = NewReader(w.Packet())
	if r.U8() != 0 || r.U16() != 1 || r.I32() != 1002 || r.I32() != 1 {
		t.Fatal("skill snapshot")
	}
	for i := 0; i < 5; i++ {
		if r.U16() != 0 {
			t.Fatal("empty skill section")
		}
	}
	if r.U16() != 1 || r.I32() != 1002 || r.I32() != 60 || r.Remaining() != 0 {
		t.Fatal("cooldown snapshot")
	}
}
