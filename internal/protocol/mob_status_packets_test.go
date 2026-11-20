package protocol

import (
	"bytes"
	"testing"
	"time"

	"maple-go-server/internal/content"
)

func TestMobStatMaskUsesNativeCFlagDwordOrder(t *testing.T) {
	w := NewRawWriter()
	mobStatMask(w, []MobStat{MobPAD, MobPoison, MobMagicCrash})
	b := w.Packet()
	if len(b) != 32 {
		t.Fatal(len(b))
	}
	// index9 -> dword7 bit9, index36 -> dword6 bit4.
	want := make([]byte, 32)
	want[28], want[29] = 0x01, 0x02
	want[24] = 0x10
	if !bytes.Equal(b, want) {
		t.Fatalf("mask %x", b)
	}
}

func TestApplyMonsterStatusOrderTimeAndMovementTail(t *testing.T) {
	now := time.Unix(1000, 0)
	p := ApplyMonsterStatus(77, []MobStatValue{
		{Stat: MobSpeed, Value: -40, Reason: 2201004, ExpiresAt: now.Add(4 * time.Second)},
		{Stat: MobPoison, Value: 25, Reason: 2101005, ExpiresAt: now.Add(1500 * time.Millisecond)},
		{Stat: MobDoom, Value: 1, Reason: 2311005, ExpiresAt: now.Add(time.Second)},
		{Stat: MobPImmune, Value: 1, Reason: 0, ExpiresAt: now.Add(time.Second)},
	}, 0, now)
	r := NewReader(p)
	if r.U16() != SendApplyMonsterStatus || r.I32() != 77 {
		t.Fatal("header")
	}
	r.Skip(32)
	// TempStatDecode order: 6 Speed, 9 Poison, 18 PImmune, 16 Doom.
	expect := []struct {
		v, src int32
		units  uint16
	}{{-40, 2201004, 8}, {25, 2101005, 3}, {1, 0, 2}, {1, 2311005, 2}}
	for i, e := range expect {
		if r.I32() != e.v || r.I32() != e.src || r.U16() != e.units {
			t.Fatalf("stat %d out of native order: %x", i, p)
		}
	}
	if r.U16() != 0 || r.U8() != 1 || r.U8() != 0 || r.Remaining() != 0 {
		t.Fatalf("tail %x", p)
	}
	if ApplyMonsterStatus(77, []MobStatValue{{Stat: MobBurned}}, 0, now) != nil || ApplyMonsterStatus(77, []MobStatValue{{Stat: MobSpeed}, {Stat: MobSpeed}}, 0, now) != nil {
		t.Fatal("unreviewed or duplicate stat encoded")
	}
}

func TestCancelMonsterStatusAndSpawnCarryStats(t *testing.T) {
	p := CancelMonsterStatus(5, []MobStat{MobPoison})
	if len(p) != 2+4+32+1 {
		t.Fatalf("cancel poison has no movement byte: %x", p)
	}
	if p := CancelMonsterStatus(5, []MobStat{MobStun}); len(p) != 2+4+32+2 {
		t.Fatalf("cancel stun movement byte: %x", p)
	}
	life := content.Life{TemplateID: 100100, X: 3, Y: 4}
	plain := SpawnMonster(9, life, false)
	now := time.Unix(1, 0)
	with := SpawnMonsterWithStats(9, life, false, []MobStatValue{{Stat: MobPoison, Value: 3, Reason: 2101005, ExpiresAt: now.Add(time.Second)}}, now)
	if len(with) != len(plain)+10 || !bytes.Equal(with[:11], plain[:11]) {
		t.Fatalf("spawn stats layout %x vs %x", with, plain)
	}
}

func TestV148BurnedInfoSetAndReset(t *testing.T) {
	now := time.Unix(1000, 0)
	burn := MobBurn{OwnerID: 4916, SkillID: 4340012, Damage: 810, Interval: time.Second,
		ExpiresAt: now.Add(8 * time.Second), RemainingTicks: 8, Stacks: 3}
	flagStun := MobStatValue{Stat: MobStun, Value: 1, Reason: 4321006, ExpiresAt: now.Add(time.Second)}
	apply := ApplyMonsterStatus(77, []MobStatValue{flagStun}, 0, now, burn)
	r := NewReader(apply)
	if r.U16() != 0x1b1 || r.I32() != 77 {
		t.Fatalf("apply header %x", apply)
	}
	r.Skip(28)
	if r.U32() != 1<<27|1<<7 || r.I32() != 1 || r.I32() != 4321006 || r.U16() != 2 || r.U8() != 1 {
		t.Fatalf("mixed status mask, stun triple or burned list count: %x", apply)
	}
	for i, expected := range []int32{4916, 4340012, 810, 1000, 1008000, 8, 3} {
		if actual := r.I32(); actual != expected {
			t.Fatalf("burn field %d = %d, want %d", i, actual, expected)
		}
	}
	if r.U16() != 0 || r.U8() != 1 || r.U8() != 0 || r.Remaining() != 0 {
		t.Fatalf("apply suffix %x", apply)
	}
	reset := CancelMonsterBurns(77, 0, []MobBurn{burn})
	r = NewReader(reset)
	if r.U16() != 0x1b2 || r.I32() != 77 {
		t.Fatalf("reset header %x", reset)
	}
	r.Skip(28)
	if r.U32() != 1<<27 || r.I32() != 0 || r.I32() != 1 || r.I32() != 4916 ||
		r.U32() != 4340012<<4|3 || r.U8() != 1 || r.Remaining() != 0 {
		t.Fatalf("reset burned owner and packed source: %x", reset)
	}
}
