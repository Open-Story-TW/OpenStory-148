package protocol

import (
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

func TestGenericAttackDecodesNativeMagicAndMeleeFrames(t *testing.T) {
	for _, raw := range []string{nativeSnailMiss, nativeSnailHit} {
		body := v148Body(RecvMagicAttack, raw)
		want, err := ReadBeginnerMagicAttack(NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		a, err := ReadAttack(RecvMagicAttack, NewReader(body))
		if err != nil || a.SkillID != 1000 || a.Tick != want.Tick || a.X != want.X || a.Y != want.Y || a.Display != want.Display || a.Speed != want.Speed || len(a.Targets) != len(want.Targets) || hex.EncodeToString(a.Wire) != hex.EncodeToString(body) {
			t.Fatalf("generic magic %+v %v", a, err)
		}
		for i := range a.Targets {
			if a.Targets[i].ObjectID != want.Targets[i].ObjectID || a.Targets[i].Lines[0].Raw != want.Targets[i].Lines[0].Raw {
				t.Fatal("generic magic target shifted")
			}
		}
		for n := 0; n < len(body); n++ {
			if _, err = ReadAttack(RecvMagicAttack, NewReader(body[:n])); err == nil {
				t.Fatalf("truncated magic accepted %d", n)
			}
		}
	}
	body := physicalMeleeBody(1001005, 1, 2, 3)
	want, _ := ReadMeleeAttack(NewReader(body))
	a, err := ReadAttack(RecvMeleeAttack, NewReader(body))
	if err != nil || a.SkillID != 1001005 || len(a.Targets) != 3 || a.Tick != want.Tick || int(a.X) != want.X || a.Speed != want.Speed || a.Display != want.Display || a.Targets[2].Lines[0].Damage != 12 {
		t.Fatalf("generic melee %+v %v", a, err)
	}
	if _, err = ReadAttack(RecvMeleeAttack, NewReader(physicalMeleeBody(1001005, 1, 1))); err == nil {
		t.Fatal("duplicate target accepted")
	}
	// v148 body attacks lack the extra melee i32 after the tick (offset 28).
	bodyAttack := append(append([]byte(nil), body[:28]...), body[32:]...)
	if b, err := ReadAttack(RecvBodyAttack, NewReader(bodyAttack)); err != nil || b.Opcode != RecvBodyAttack || len(b.Targets) != 3 {
		t.Fatalf("body attack uses the close-range layout: %+v %v", b, err)
	}
}

func shootBody(skill int, hits int, ids ...int32) []byte {
	w := NewRawWriter()
	w.U8(0)
	w.U8(byte(len(ids)<<4) | byte(hits))
	w.I32(int32(skill))
	w.Zero(6)
	if QuirkFor(skill).KeyDown {
		w.I32(900)
	}
	w.U8(0)
	w.U16(0x8021)
	w.Zero(5)
	w.U8(5)
	w.U32(1234)
	w.U32(0)
	w.I16(3)
	w.I16(0)
	w.U8(0)
	for _, id := range ids {
		w.I32(id)
		w.Zero(18)
		for i := 0; i < hits; i++ {
			w.U32(0x80000000 | 77)
		}
		w.Zero(8)
	}
	w.I16(-5)
	w.I16(40)
	w.Zero(4)
	return w.Packet()
}

func TestGenericShootAttackAmmoSlotKeyDownAndRemoteBullet(t *testing.T) {
	a, err := ReadAttack(RecvShootAttack, NewReader(shootBody(3001004, 2, 9, 10)))
	if err != nil || a.Slot != 3 || len(a.Targets) != 2 || a.LineCount() != 2 || !a.Targets[1].Lines[1].Critical() || a.Targets[1].Lines[1].Claimed() != 77 || a.X != -5 || !a.FacingLeft() {
		t.Fatalf("shoot %+v %v", a, err)
	}
	k, err := ReadAttack(RecvShootAttack, NewReader(shootBody(3121004, 1, 9)))
	if err != nil || k.Charge != 900 {
		t.Fatalf("keydown shoot %+v %v", k, err)
	}
	a.Targets[0].Lines[0].Damage = 50
	p := RemoteAttackPacket(domain.Character{ID: 5, Level: 30}, RemoteAttack{Opcode: SendShootAttack, SkillLevel: 4, Mastery: 10, BulletItem: 2060000, Attack: a})
	r := NewReader(p)
	if r.U16() != SendShootAttack || r.I32() != 5 || r.U8() != 0x22 || r.U8() != 30 || r.U8() != 4 || r.I32() != 3001004 || r.U8() != 0 || r.U16() != 0x8021 || r.U8() != 5 || r.U8() != 10 || r.I32() != 2060000 {
		t.Fatalf("remote shoot header %x", p)
	}
	if r.I32() != 9 || r.U8() != 7 || r.U32() != 0x80000000|50 || r.U32() != 0x80000000|77 {
		t.Fatal("remote shoot first target lines")
	}
	r.Skip(4 + 1 + 8)
	if r.I16() != -5 || r.I16() != 40 || r.Remaining() != 0 {
		t.Fatalf("remote shoot position/tail %x", p)
	}
}

func TestRemoteMagicMatchesNativeBeginnerOutput(t *testing.T) {
	body := v148Body(RecvMagicAttack, nativeSnailHit)
	a, _ := ReadAttack(RecvMagicAttack, NewReader(body))
	a.Targets[0].Lines[0].Damage = 5
	old, _ := ReadBeginnerMagicAttack(NewReader(body))
	old.Targets[0].Lines[0].Damage = 5
	c := domain.Character{ID: 102, Level: 3}
	got := RemoteAttackPacket(c, RemoteAttack{Opcode: SendMagicAttack, SkillLevel: 1, BulletItem: -1, Attack: a})
	if hex.EncodeToString(got) != hex.EncodeToString(MagicAttackPacket(c, 1, old)) {
		t.Fatalf("generic remote magic %x", got)
	}
}

// Wild Hunter shots carry a trailing I16 position (MCP, R2 2026-11-02).
func TestReadShootAttackWildHunterTail(t *testing.T) {
	for _, c := range []struct {
		hex            string
		skill, targets int
		lines          int
		tailX, tailY   int16
	}{
		{"4c000113288ef70100ea2e3f4a02002480f7f4bd42040405a31a070000000000000000415e420f000780070186fd1f0188fd1f010702bd000000c6000000350100008f18bfe656d627e827ff1f010600daffe6fedd00", 33001000, 1, 3, -282, 221},
		{"4c000111ca14f90100230ccc600200d68045095a940404839f180700000000000000000050420f000700000180001f017f001f01a701c60200008f18bfe60fab865b89001f010200dbff89000301", 33101002, 1, 1, 137, 259},
		{"4c00012be962f90100504c798c02000601e9a6af0a04041b44180700000000000000000050420f000700010109001f0109001f01410a020200003f0100005c01000062010000e60100003f0100003f0100005c01000062010000e6010000e60500008f18bfe6d8eec31d4f420f00070100011a0127011a012701410a20020000ec010000d401000049010000ec010000d401000049010000ec010000d401000049010000180900008f18bfe60bcf6dbed9ff1f01fdffdcffd9ffde00", 33121001, 2, 11, -39, 222},
		{"4c000101c914f901005603af2102002400f7f4bd42040473281807000000000000000027d9ff1f01faffdaff0000e200", 33101001, 0, 1, 0, 226},
	} {
		b := v148Wire(RecvShootAttack, c.hex)
		a, err := ReadAttack(RecvShootAttack, NewReader(b[2:]))
		if err != nil || a.SkillID != c.skill || len(a.Targets) != c.targets || a.LineCount() != c.lines || a.TailX != c.tailX || a.TailY != c.tailY {
			t.Fatalf("skill %d: %+v %v", c.skill, a, err)
		}
	}
}

// Evan magic flags a trailing dragon position (world1 samples, 2026-11-02).
func TestReadMagicAttackEvanTail(t *testing.T) {
	for _, c := range []struct {
		hex                   string
		skill, targets, lines int
		tailX, tailY          int16
	}{
		{"011269b54f010066eca63400a88047bdfe100604730b38070000000066420f00078006014efd1f014efd1f01d000f9010000fb0200008f18bfe6f857700172fd1f010178fd1f01", 22001001, 1, 2, -648, 287},
		{"011174fb510100aa4835a3002b8051955cc90604810c38070000000066420f00070700014dfd1f0151fd1f01cd00280300008f18bfe6ba7025e971fd1f010176fd1f01", 22150004, 1, 1, -650, 287},
		{"0111083c510100f0ca442900af8062b009730604e71e38070000000066420f0007000701f9fd1f01f8fd1f019e00e40200008f18bfe6f59edfcd1dfe1f01011ffe1f01", 22101000, 1, 1, -481, 287},
		{"011118635101000062739e00a9809c236a0f0604d72e38070000000066420f000700020178fe1f0177fe1f01b6009e0300008f18bfe6f639d29dccfe1f0101c7fe1f01", 22111000, 1, 1, -313, 287},
	} {
		b := v148Body(RecvMagicAttack, c.hex)
		a, err := ReadAttack(RecvMagicAttack, NewReader(b))
		if err != nil || a.SkillID != c.skill || len(a.Targets) != c.targets || a.LineCount() != c.lines || a.TailX != c.tailX || a.TailY != c.tailY {
			t.Fatalf("skill %d: %+v %v", c.skill, a, err)
		}
	}
}

// Meso Explosion with targets (live 4211006 frame, 2026-11-02): I32 lines,
// 8-byte trailers, then position, drops (I32 + 2 bytes) and a U16 tail.
func TestReadMesoExplosionLiveTargets(t *testing.T) {
	b := v148Body(RecvMeleeAttack, "02453e414000005a204956004e8013a043470104c3b5c10700000000d4420f0007810001cdffcefdcdffcefd01e702000061ca07c8af220efcda420f00078100014effcefd4effcefd053e020000d80100000e0300003e0200008b02000061ca07c88dbbd2cfd5420f00078003014effcefd4effcefd05ea0100005d020000ff010000ee0100008002000061ca07c8f31ad0b1fa420f000780000153ffcefd53ffcefd052202000007020000ae02000022020000e501000061ca07c8270dc39d95ffcefd0511d09a3b0f0012d09a3b0e0013d09a3b0e0014d09a3b0e0015d09a3b0e006302")
	a, err := ReadAttack(RecvMeleeAttack, NewReader(b))
	if err != nil || a.SkillID != 4211006 || len(a.Targets) != 4 || len(a.MesoDrops) != 5 || a.MesoDrops[0] != 1000001553 || a.X != -107 || a.Y != -562 {
		t.Fatalf("%+v %v", a, err)
	}
	if a.Targets[0].Lines[0].Damage != 743 || len(a.Targets[1].Lines) != 5 {
		t.Fatalf("lines %+v", a.Targets)
	}
}
