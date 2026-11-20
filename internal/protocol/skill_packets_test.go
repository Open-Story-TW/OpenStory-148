package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestV148RegionalMapleWarriorCastPosition(t *testing.T) {
	// Real 5921000 at rank 30, with caster at (-185, 215) and a 1000ms delay.
	wire, err := hex.DecodeString("a3004751a001e8585a001e47ffd70080e80300")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{5721000, 5821000, 5921000, 5221000} {
		binary.LittleEndian.PutUint32(wire[6:10], uint32(id))
		cast, err := ReadSkillCast(NewReader(wire[2:]))
		if err != nil || cast.SkillID != id || cast.Level != 30 || !cast.HasPosition || cast.X != -185 || cast.Y != 215 ||
			cast.Affected != 0x80 || cast.ActionDelay != 1000 || cast.HasMobs {
			t.Fatalf("cast %d: %+v, %v", id, cast, err)
		}
	}
}

func TestV148MihilePartyCastsKeepTheOriginalPositionAndAffectedByte(t *testing.T) {
	for _, tc := range []struct {
		wire  string
		skill int
		level int
		x     int16
		delay uint16
	}{
		{"a3007ca5b7014cbd0b031415ffd70030000000", 51101004, 20, -235, 0},
		{"a3005ee1b7016d0b0c031e0000d70030e80300", 51121005, 30, 0, 1000},
	} {
		wire, err := hex.DecodeString(tc.wire)
		if err != nil {
			t.Fatal(err)
		}
		cast, err := ReadSkillCast(NewReader(wire[2:]))
		if err != nil || cast.SkillID != tc.skill || cast.Level != tc.level || !cast.HasPosition || cast.X != tc.x ||
			cast.Y != 215 || cast.Affected != 0x30 || cast.ActionDelay != tc.delay || cast.HasMobs {
			t.Errorf("native Mihile cast %d: %+v, %v", tc.skill, cast, err)
		}
	}
}

func TestV148SpiritLinkUsesTheLearnedSourceInTheSharedTriplet(t *testing.T) {
	now := time.Unix(500, 0)
	for _, tc := range []struct{ summon, linked int }{{3111005, 3120006}, {3211005, 3220005}} {
		rows := []domain.SkillBuff{
			{ID: tc.summon, Stat: "COMBO", Value: 1, ExpiresAt: now.Add(time.Second)},
			{ID: tc.summon, Stat: "SPIRIT_SURGE", Value: 1, ExpiresAt: now.Add(time.Second)},
		}
		readMask := func(r *Reader) {
			t.Helper()
			for idx, want := range [8]uint32{2: 1 << 9, 7: 1 << 21} {
				if got := r.U32(); got != want {
					t.Fatalf("summon %d mask word %d: got %x want %x", tc.summon, idx, got, want)
				}
			}
		}
		r := NewReader(SkillBuffs(rows, now))
		if r.U16() != 0x2d {
			t.Fatal("local stat set missing")
		}
		readMask(r)
		if r.I32() != 1 || r.I32() != int32(tc.summon) || r.I32() != 1000 {
			t.Fatalf("summon %d slot lost its original cancellation source", tc.summon)
		}
		if r.I32() != 1 || r.I32() != int32(tc.linked) || r.I32() != 1000 || r.Err() != nil {
			t.Fatalf("summon %d learned Spirit Link source missing from I32 triplet", tc.summon)
		}
		r = NewReader(ForeignSkillBuffs(7, rows))
		if r.U16() != 0x145 || r.I32() != 7 {
			t.Fatal("remote stat set missing")
		}
		readMask(r)
		if r.U8() != 1 || r.U16() != 1 || r.I32() != int32(tc.linked) || r.Err() != nil {
			t.Fatalf("summon %d learned Spirit Link source missing from remote I16/I32 fields", tc.summon)
		}
		r = NewReader(CancelSkillBuffs(0, rows))
		if r.U16() != 0x2e {
			t.Fatal("local stat reset missing")
		}
		readMask(r)
	}
}

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
