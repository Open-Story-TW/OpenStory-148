package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"maple-go-server/internal/domain"
	"testing"
)

func TestNativeRemoveSummonExactObjectAndOwnerPacket(t *testing.T) {
	// 0x6DB45D emits exactly one object DWORD after opcode0x103.
	for _, body := range [][]byte{{0x64, 0x42, 0x0f, 0}, {0}, {0, 0, 0, 0}, {0xff, 0xff, 0xff, 0xff}, {0x64, 0x42, 0x0f, 0, 0}} {
		oid, err := ReadRemoveSummon(NewReader(body))
		valid := len(body) == 4 && binary.LittleEndian.Uint32(body) == 1000036
		if valid && (err != nil || oid != 1000036) || !valid && err == nil {
			t.Fatalf("body%x oid%d err%v", body, oid, err)
		}
	}
	// 0x6E8A59 consumes the owner; 0x6E8597 consumes object and6E0D4B reason.
	got := RemoveSummon(4904, 1000036, SummonLeaveAnimated)
	want := "a2012813000064420f0004"
	if hex.EncodeToString(got) != want {
		t.Fatalf("remove packet%x want%s", got, want)
	}
}

func TestSpawnSummonMatchesNativeCreateOrder(t *testing.T) {
	p := SpawnSummon(SummonView{OwnerID: 7, ObjectID: 900, SkillID: 3111005, OwnerLevel: 70, SkillLevel: 3, X: -10, Y: 20, Stance: 4, MoveAbility: 4, AssistType: 1, Animated: true})
	r := NewReader(p)
	// 0x6E88A1: owner, oid, skill, U8, U8; 0x6E58A2: x, y, stance, fh, move, assist, enter, U8, avatar flag.
	if r.U16() != SendSpawnSummon || r.I32() != 7 || r.I32() != 900 || r.I32() != 3111005 || r.U8() != 69 || r.U8() != 3 || r.I16() != -10 || r.I16() != 20 || r.U8() != 4 || r.I16() != 0 || r.U8() != 4 || r.U8() != 1 || r.U8() != 1 || r.U8() != 1 || r.U8() != 0 || r.Remaining() != 0 {
		t.Fatalf("spawn summon %x", p)
	}
	if SpawnSummon(SummonView{OwnerID: 7, ObjectID: 1, SkillID: 4341006, OwnerLevel: 1, SkillLevel: 1}) != nil {
		t.Fatal("avatar summon encoded without its look")
	}
}

// nativeSummonAttack is world0's summon_attack_in for 3101007 (goqa13,
// 2026-11-01): one target 1000001, damage 5.
const nativeSummonAttack = "87420f00fecd4905040106fe1f011dfe00010000000041420f00048701000780060139ff1f013aff1f016400050000005d32e2a2"

func TestSummonAttackRetainsCriticalFlagWithoutCountingItAsDamage(t *testing.T) {
	body := v148Summon(nativeSummonAttack)
	// Native header22, target oid4 + v148 template4 + metadata18, then the raw damage DWORD.
	binary.LittleEndian.PutUint32(body[48:52], 0x80000005)
	in, err := ReadSummonAttack(NewReader(body))
	if err != nil || len(in.Targets) != 1 || len(in.Targets[0].Lines) != 1 {
		t.Fatal("critical summon frame lost its complete target", err)
	}
	line := in.Targets[0].Lines[0]
	if line.Raw != 0x80000005 || !line.Critical() || line.Claimed() != 5 || line.Damage != 5 {
		t.Fatalf("critical flag was discarded or counted as damage: %+v", line)
	}
}

func TestSummonAttackRoundTrip(t *testing.T) {
	body := v148Summon(nativeSummonAttack)
	in, err := ReadSummonAttack(NewReader(body))
	if err != nil || in.ObjectID != 1000071 || in.Action != 4 || len(in.Targets) != 1 || in.Targets[0].ObjectID != 1000001 || in.Targets[0].Lines[0].Damage != 5 || in.X != -506 || in.Y != 287 {
		t.Fatalf("native summon attack %+v %v", in, err)
	}
	p := SummonAttackPacket(7, 900, 70, 0x83, []SummonHit{{11, 50}, {12, 0}})
	r := NewReader(p)
	if r.U16() != SendSummonAttack || r.I32() != 7 || r.I32() != 900 || r.U8() != 69 || r.U8() != 0x83 || r.U8() != 2 || r.I32() != 11 || r.U8() != 7 || r.I32() != 50 {
		t.Fatalf("summon attack out %x", p)
	}
	r.Skip(9)
	if r.U8() != 0 || r.Remaining() != 0 {
		t.Fatal("summon attack tail")
	}
	if d := DamageSummonPacket(7, 900, -2, 10, 0, false); len(d) != 2+4+4+1+4 {
		t.Fatalf("index -2 has no mob fields: %x", d)
	}
}

// Mirrored Target 4341006 carries the owner avatar (Java addCharLook).
func TestSpawnSummonMirroredTargetLook(t *testing.T) {
	v := SummonView{OwnerID: 7, ObjectID: 9, SkillID: 4341006, OwnerLevel: 70, SkillLevel: 10, Stance: 4}
	if SpawnSummon(v) != nil {
		t.Fatal("mirror without a look")
	}
	look := domain.Character{ID: 7, Name: "Dual", Gender: 0, Skin: 0, Face: 20000, Hair: 30000}
	v.Look = &look
	p := SpawnSummon(v)
	plain := SpawnSummon(SummonView{OwnerID: 7, ObjectID: 9, SkillID: 3101007, OwnerLevel: 70, SkillLevel: 10, Stance: 4})
	w := NewWriter(0)
	addCharLook(w, look)
	lookBytes := w.Packet()[2:]
	if len(p) != len(plain)+len(lookBytes) || p[len(plain)-1] != 1 {
		t.Fatalf("mirror layout % x", p)
	}
}
