package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"maple-go-server/internal/domain"
	"slices"
	"testing"
)

func TestV148SatelliteRetainsOriginalInterleavedDamageRecords(t *testing.T) {
	const wire = "200148420f005982040184041cffd70015ffd7000000000049420f00706f9800070100706f98000132ffd70032ffd7006801090d0000173538ba4a420f00706f9800070100706f98000152ffd70052ffd70068014b0500001deb7a0449420f00706f9800070100706f98000132ffd70032ffd7006801930c0000173538ba4a420f00706f9800070100706f98000152ffd70052ffd70068010e0f00001deb7a04"
	packet, err := hex.DecodeString(wire)
	if err != nil {
		t.Fatal(err)
	}
	in, err := ReadSummonAttack(NewReader(packet[2:]))
	if err != nil || in.ObjectID != 1000008 || in.Tick != 17072729 || !slices.Equal(in.HitOrder, []int{0, 1, 0, 1}) || !slices.Equal(in.Wire, packet[2:]) || len(in.Targets) != 2 {
		t.Fatalf("original Mechanic satellite packet: %+v (%v)", in, err)
	}
	for index, want := range [][2]int{{3337, 3219}, {1355, 3854}} {
		target := in.Targets[index]
		if target.ObjectID != int32(1000009+index) || len(target.Lines) != 2 || target.Lines[0].Damage != want[0] || target.Lines[1].Damage != want[1] {
			t.Fatalf("target %d %+v", index, target)
		}
	}
}

func TestV148SG88ReadsOriginalChargeGauge(t *testing.T) {
	const wire = "20015d420f0081920902840115ffd70015ffd700280000005c420f00706f9800070100706f9800016600d7006600d7000000012d0000b2aff972"
	packet, err := hex.DecodeString(wire)
	if err != nil {
		t.Fatal(err)
	}
	in, err := ReadSummonAttack(NewReader(packet[2:]))
	if err != nil || in.ObjectID != 1000029 || in.ChargeGauge != 40 || len(in.Targets) != 1 {
		t.Fatalf("original SG-88 attack: %+v (%v)", in, err)
	}
	target := in.Targets[0]
	if target.ObjectID != 1000028 || len(target.Lines) != 1 || target.Lines[0].Damage != 11521 {
		t.Fatalf("original charged SG-88 target: %+v", target)
	}
}

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

func TestReadSummonAttackTeslaTriangleLiveCapture(t *testing.T) {
	// Live TWMS v148 35111002 Magnetic Field 0x120 body (68 bytes = 10 hdr + 12 coil IDs + 12 pos/charge + 34 target).
	rawHex := "bf420f00558179020701bf420f00bd420f00be420f00b6fed700b6fed70000000000bc420f00706f9800070100706f98000115ffd70015ffd7001b00e930af2fea89baf1"
	body := make([]byte, len(rawHex)/2)
	for i := range body {
		var b byte
		for _, ch := range rawHex[2*i : 2*i+2] {
			b <<= 4
			if ch >= 'a' {
				b |= byte(ch-'a') + 10
			} else {
				b |= byte(ch - '0')
			}
		}
		body[i] = b
	}
	in, err := ReadSummonAttack(NewReader(body))
	if err != nil {
		t.Fatalf("ReadSummonAttack tesla failed: %v", err)
	}
	if in.ObjectID != 1000127 || in.Action != 7 || in.TeslaCoils != [3]int32{1000127, 1000125, 1000126} || in.X != -330 || in.Y != 215 {
		t.Fatalf("unexpected tesla header: %+v", in)
	}
	if len(in.Targets) != 1 || in.Targets[0].ObjectID != 1000124 || len(in.Targets[0].Lines) != 1 || in.Targets[0].Lines[0].Raw != 0x2faf30e9 {
		t.Fatalf("unexpected tesla target: %+v", in.Targets)
	}
}
