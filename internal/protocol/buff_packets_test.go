package protocol

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
)

func TestTemporaryStatSetMultiStatClientOrder(t *testing.T) {
	p, err := TemporaryStatSet(1301007, 30*time.Second, []BuffStat{{Stat: "MAXMP", Value: 60}, {Stat: "MAXHP", Value: 61}})
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(p)
	if r.U16() != 0x2d {
		t.Fatal("opcode")
	}
	var mask [8]uint32
	for i := range mask {
		mask[i] = r.U32()
	}
	if mask[7] != 1<<13|1<<14 {
		t.Fatalf("mask %x", mask)
	}
	// encodeForLocal reads MAXHP (order 27) before MAXMP (order 28).
	for _, want := range []int16{61, 60} {
		if v, source, ms := r.I16(), r.I32(), r.I32(); v != want || source != 1301007 || ms != 30000 {
			t.Fatalf("triplet %d %d %d", v, source, ms)
		}
	}
	r.Skip(3)
	r.U16()
	r.U8() // v148 byte after the delay
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("layout %v %d", r.Err(), r.Remaining())
	}
	if p, _ = ForeignTemporaryStatSet(9, 1301007, []BuffStat{{Stat: "MAXHP", Value: 61}, {Stat: "MAXMP", Value: 60}}); len(p) != 2+4+32+3+2 {
		t.Fatalf("remote clients decode neither max stat: %d", len(p))
	}
}

func TestTemporaryStatSetWideValuesAndRemotePayloads(t *testing.T) {
	p, err := TemporaryStatSet(23101003, time.Second, []BuffStat{{Stat: "SPIRIT_SURGE", Value: 70000}, {Stat: "WATK", Value: 5}})
	if err != nil {
		t.Fatal(err)
	}
	// WATK (order 5) precedes SPIRIT_SURGE (order 160); both values are I32.
	if want := 2 + 32 + 2*12 + 3 + 2 + 1 + 1; len(p) != want { // v148: SPIRIT_SURGE also reads the movement byte
		t.Fatalf("length %d want %d", len(p), want)
	}
	if _, err = TemporaryStatSet(1, time.Second, []BuffStat{{Stat: "WATK", Value: 40000}}); err == nil {
		t.Fatal("I16 overflow accepted")
	}
	p, err = ForeignTemporaryStatSet(5, 2001002, []BuffStat{{Stat: "SPEED", Value: 20}, {Stat: "DARK_SIGHT", Value: 1}})
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(p)
	r.U16()
	r.I32()
	r.Skip(32)
	if r.U8() != 20 { // SPEED U8; DARK_SIGHT has no payload
		t.Fatal("speed payload")
	}
	r.Skip(5)
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatal("remote layout")
	}
	if _, err = ForeignTemporaryStatSet(5, 1, []BuffStat{{Stat: "POISON", Value: 1}}); err == nil {
		t.Fatal("unreviewed remote payload accepted")
	}
	if _, err = TemporaryStatReset(5, []buffstat.Stat{"BIT_999"}); err == nil {
		t.Fatal("unknown reset accepted")
	}
}

func TestPhantomCloakV148TemporaryStat(t *testing.T) {
	const skillID = 20031205
	stats := []BuffStat{{Stat: "PHANTOM_CLOAK", Value: 1}}
	local, err := TemporaryStatSet(skillID, 2*time.Second, stats)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := ForeignTemporaryStatSet(17, skillID, stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, packet := range []struct {
		name string
		data []byte
		want uint16
	}{
		{"local", local, 0x2d},
		{"remote", remote, 0x145},
	} {
		t.Run(packet.name, func(t *testing.T) {
			r := NewReader(packet.data)
			if r.U16() != packet.want {
				t.Fatal("wrong v148 temporary stat opcode")
			}
			if packet.name == "remote" && r.I32() != 17 {
				t.Fatal("wrong cloak owner")
			}
			var mask [8]uint32
			for i := range mask {
				mask[i] = r.U32()
			}
			if mask != [8]uint32{0, 1 << 27} || r.I16() != 1 || r.I32() != skillID {
				t.Fatalf("cloak mask/source fields mismatch: %x", packet.data)
			}
			tail := 5
			if packet.name == "local" {
				if r.I32() != 2000 {
					t.Fatal("incorrect WZ cloak lifetime")
				}
				tail = 6
			}
			if r.Err() != nil || r.Remaining() != tail {
				t.Fatalf("wrong trailing fields: remaining %d, %v", r.Remaining(), r.Err())
			}
		})
	}
}

// Two-state and indie layouts follow Open-Maple docs/server/baseline/buffstat-native.json.
func TestTwoStateAndIndieLayouts(t *testing.T) {
	p, err := TemporaryStatSet(5121009, 10*time.Second, []BuffStat{{Stat: "SPEED_INFUSION", Value: 30}, {Stat: "DASH_SPEED", Value: 20}})
	if err != nil {
		t.Fatal(err)
	}
	r := NewReader(p)
	r.U16()
	r.Skip(32 + 3)
	// DASH_SPEED (249) precedes SPEED_INFUSION (252): base + term, then
	// base + time + term.
	if v, src := r.I32(), r.I32(); v != 20 || src != 5121009 {
		t.Fatalf("dash base %d %d", v, src)
	}
	r.Skip(5)
	if r.U16() != 10 {
		t.Fatal("dash term")
	}
	if r.I32() != 30 {
		t.Fatal("infusion value")
	}
	r.Skip(4 + 5 + 5)
	if r.U16() != 10 {
		t.Fatal("infusion term")
	}
	r.U16()                                           // delay
	r.U8()                                            // v148 byte after the delay
	if r.U8(); r.Err() != nil || r.Remaining() != 0 { // DASH_SPEED movement byte
		t.Fatalf("layout %v %d", r.Err(), r.Remaining())
	}
	p, err = TemporaryStatSet(2301004, time.Minute, []BuffStat{{Stat: "MP_BOOST", Value: 300}, {Stat: "HP_BOOST", Value: 200}})
	if err != nil {
		t.Fatal(err)
	}
	r = NewReader(p)
	r.U16()
	r.Skip(32 + 3)
	// HP_BOOST (indie #5) precedes MP_BOOST (indie #6).
	for _, want := range []int32{200, 300} {
		if n, src, v := r.I32(), r.I32(), r.I32(); n != 1 || src != 2301004 || v != want {
			t.Fatalf("indie entry %d %d %d", n, src, v)
		}
		r.I32()
		if r.I32() != 60000 {
			t.Fatal("indie duration")
		}
	}
	r.U16()
	r.U8() // v148 byte after the delay
	if r.Err() != nil || r.Remaining() != 0 {
		t.Fatalf("indie layout %v %d", r.Err(), r.Remaining())
	}
	if p, _ = ForeignTemporaryStatSet(9, 2301004, []BuffStat{{Stat: "HP_BOOST", Value: 200}}); len(p) != 2+4+32+3+2 {
		t.Fatalf("remote indie must carry no payload: %d", len(p))
	}
}

func TestTailFieldsAreZeroFilledInNativeOrder(t *testing.T) {
	// DARK_AURA (109, triplet #121 + tail U8 #4) and BODY_BOOST (112, tail I32 #8).
	p, err := TemporaryStatSet(32001003, time.Minute, []BuffStat{{Stat: "BODY_BOOST", Value: 1}, {Stat: "DARK_AURA", Value: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if want := 2 + 32 + 2*10 + 3 + 1 + 4 + 2 + 1; len(p) != want {
		t.Fatalf("length %d want %d", len(p), want)
	}
}

// v148 SecondaryStat::DecodeForLocal (0x713F18) reads a byte for SUMMON/COMBO
// (bit 21) in the tail section; Silver Hawk 3111005 hit EOF without it.
func TestSummonBuffCarriesV148TailByte(t *testing.T) {
	p, err := TemporaryStatSet(3111005, 43*time.Second, []BuffStat{{Stat: "SUMMON", Value: 1}})
	if err != nil {
		t.Fatal(err)
	}
	// opcode, mask, value/source/time, three suffix bytes, bit-21 tail, delay, v148 byte.
	if want := 2 + 32 + 2 + 4 + 4 + 3 + 1 + 2 + 1; len(p) != want {
		t.Fatalf("summon buff length %d want %d: %x", len(p), want, p)
	}
}

func TestV148ComboBuffSetsIconGateTailByte(t *testing.T) {
	for _, skill := range []int{1111002, 1111001} {
		p, err := TemporaryStatSet(skill, 200*time.Second, []BuffStat{{Stat: "COMBO", Value: 1}})
		if err != nil {
			t.Fatal(err)
		}
		r := NewReader(p)
		r.U16()
		r.Skip(32 + 2 + 4 + 4 + 3)
		if tail := r.U8(); tail != 1 || r.Err() != nil {
			t.Fatalf("skill %d COMBO tail = %d err=%v", skill, tail, r.Err())
		}
	}
}

// v148 OnTemporaryStatSet reads the movement byte for ENERGY_CHARGE too
// (move mask 0x6F9246); the login Energy Charge packet hit EOF without it.
func TestEnergyChargeCarriesV148MovementByte(t *testing.T) {
	p, err := TemporaryStatSet(5110001, 0, []BuffStat{{Stat: "ENERGY_CHARGE", Value: 4400}})
	if err != nil {
		t.Fatal(err)
	}
	// opcode, mask, suffix, two-state (value, source, u8, i32, term), delay, v148 byte, movement byte.
	if want := 2 + 32 + 3 + 4 + 4 + 1 + 4 + 2 + 2 + 1 + 1; len(p) != want {
		t.Fatalf("energy charge length %d want %d", len(p), want)
	}
}

func TestV148BlueAuraSourceRankAndOwnerTail(t *testing.T) {
	now := time.Now()
	for _, rank := range []struct{ id, level int }{{32111012, 20}, {32110000, 10}} {
		rows := []domain.SkillBuff{{ID: rank.id, Level: rank.level, Stat: "BLUE_AURA", Value: rank.level, ExpiresAt: now.Add(time.Minute), SourceCharacterID: 41}}
		for _, holder := range []int64{41, 42} {
			r := NewReader(SkillBuffsFor(domain.Character{ID: holder, SkillBuffs: rows}, rows, now))
			if got := r.U16(); got != 0x2d {
				t.Fatalf("local Blue Aura opcode %#x", got)
			}
			r.Skip(32)
			if level, id, duration := int(r.I16()), int(r.I32()), r.I32(); level != rank.level || id != rank.id || duration != 60000 {
				t.Fatalf("Blue Aura triplet = %d,%d,%d", level, id, duration)
			}
			r.Skip(3)
			if owner := r.U8() != 0; owner != (holder == 41) || r.Err() != nil {
				t.Fatalf("Blue Aura owner = %t for holder %d", owner, holder)
			}
		}
	}
}
