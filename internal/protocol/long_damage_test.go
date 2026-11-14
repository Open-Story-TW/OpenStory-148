package protocol

import (
	"encoding/binary"
	"testing"
)

func withLongDamage(t *testing.T, limit int64) {
	t.Helper()
	previous := LongDamageCap()
	SetLongDamageCap(limit)
	t.Cleanup(func() { SetLongDamageCap(previous) })
}

func longTrailer(entries ...[2]int64) []byte {
	var out []byte
	for _, e := range entries {
		out = binary.LittleEndian.AppendUint32(out, uint32(e[0]))
		out = binary.LittleEndian.AppendUint64(out, uint64(e[1]))
	}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(entries)))
	return append(out, longDamageMagic...)
}

func TestAbsorbRestoresTheNativeFrameAndKeepsValues(t *testing.T) {
	withLongDamage(t, LongDamageMaxCap)
	native := []byte{0x55, 0, 1, 2, 3}
	var ledger LongDamageLedger
	got := ledger.Absorb(append(append([]byte(nil), native...), longTrailer([2]int64{0x7f000001, 5_000_000_000})...))
	if string(got) != string(native) {
		t.Fatalf("frame = %x", got)
	}
	targets := []AttackTarget{{Lines: []AttackLine{{Raw: 0x80000000 | 0x7f000001}, {Raw: 123, Damage: 123}}}}
	ledger.Apply(targets)
	first, second := targets[0].Lines[0], targets[0].Lines[1]
	if first.Claimed() != 5_000_000_000 || first.Damage != 5_000_000_000 || !first.Critical() {
		t.Fatalf("token line = %+v", first)
	}
	if second.Claimed() != 123 || second.Long != 0 {
		t.Fatalf("ordinary line = %+v", second)
	}
}

func TestApplyClaimsNothingForAnUnannouncedToken(t *testing.T) {
	withLongDamage(t, LongDamageMaxCap)
	var ledger LongDamageLedger
	targets := []AttackTarget{{Lines: []AttackLine{{Raw: 0x7f000009, Damage: 0x7f000009}}}}
	ledger.Apply(targets)
	if line := targets[0].Lines[0]; line.Claimed() != 0 || line.Damage != 0 {
		t.Fatalf("line = %+v", line)
	}
}

func TestAbsorbRejectsInvalidEntriesAndForeignFrames(t *testing.T) {
	withLongDamage(t, 1_000_000_000_000)
	var ledger LongDamageLedger
	ledger.Absorb(append([]byte{0x55, 0}, longTrailer(
		[2]int64{0x7f000001, 1_000_000_000_001}, // Over the cap.
		[2]int64{0x7f000002, -5},                // Negative.
		[2]int64{0x7f800003, 5_000_000_000},     // Server range.
		[2]int64{0x00000004, 5_000_000_000},     // Below the floor.
	)...))
	if len(ledger.values) != 0 {
		t.Fatalf("values = %v", ledger.values)
	}
	frame := append([]byte{0x6e, 0}, longTrailer([2]int64{0x7f000001, 5_000_000_000})...)
	if got := ledger.Absorb(frame); len(got) != len(frame) || len(ledger.values) != 0 {
		t.Fatal("a non-attack frame must pass through")
	}
	oversized := append([]byte{0x55, 0}, longDamageMagic...)
	oversized = append(binary.LittleEndian.AppendUint32(oversized, 99), longDamageMagic...)
	if got := ledger.Absorb(oversized); len(got) != len(oversized) {
		t.Fatal("an impossible count must pass through")
	}
}

func TestAbsorbAndApplyAreInertWhileOff(t *testing.T) {
	withLongDamage(t, 0)
	frame := append([]byte{0x55, 0}, longTrailer([2]int64{0x7f000001, 5_000_000_000})...)
	var ledger LongDamageLedger
	if got := ledger.Absorb(frame); len(got) != len(frame) {
		t.Fatal("off must not strip")
	}
	targets := []AttackTarget{{Lines: []AttackLine{{Raw: 0x7f000001, Damage: 0x7f000001}}}}
	ledger.Apply(targets)
	if line := targets[0].Lines[0]; line.Claimed() != 0x7f000001 || line.Long != 0 {
		t.Fatalf("line = %+v", line)
	}
}

func TestLedgerEvictsTheOldestValue(t *testing.T) {
	withLongDamage(t, LongDamageMaxCap)
	var ledger LongDamageLedger
	for i := range longDamageLedgerSize + 1 {
		ledger.store(longDamageTokenFloor+uint32(i), 5_000_000_000)
	}
	if _, kept := ledger.values[longDamageTokenFloor]; kept || len(ledger.values) != longDamageLedgerSize {
		t.Fatalf("size = %d", len(ledger.values))
	}
}

func TestLongDamageOutAnnouncesTokens(t *testing.T) {
	withLongDamage(t, LongDamageMaxCap)
	out := NewLongDamageOut()
	if got := out.Wire(1234); got != 1234 || out.Packet() != nil {
		t.Fatalf("small value = %d", got)
	}
	token := out.Wire(9_000_000_000)
	if token < longDamageServerFloor || token > 0x7fffffff {
		t.Fatalf("token = %#x", token)
	}
	packet := out.Packet()
	if binary.LittleEndian.Uint16(packet) != SendLongDamageValues || binary.LittleEndian.Uint16(packet[2:]) != 1 ||
		binary.LittleEndian.Uint32(packet[4:]) != token || binary.LittleEndian.Uint64(packet[8:]) != 9_000_000_000 || len(packet) != 16 {
		t.Fatalf("packet = %x", packet)
	}
}

func TestNilLongDamageOutSaturatesBelowTheTokenRange(t *testing.T) {
	var out *LongDamageOut
	if got := out.Wire(9_000_000_000); got != longDamageTokenFloor-1 {
		t.Fatalf("wire = %#x", got)
	}
	if out.Packet() != nil {
		t.Fatal("nil has no packet")
	}
}

func TestRemoteAttackUsesServerTokensAboveTheFloor(t *testing.T) {
	withLongDamage(t, LongDamageMaxCap)
	out := NewLongDamageOut()
	packet := SummonAttackPacketLong(7, 8, 30, 0, []SummonHit{{ObjectID: 9, Damage: 9_000_000_000}}, out)
	if packet == nil || out.Packet() == nil {
		t.Fatal("expected a token and its announcement")
	}
	damage := DamageMonsterLong(9, 9_000_000_000, out)
	if got := binary.LittleEndian.Uint32(damage[len(damage)-4:]); got < longDamageServerFloor {
		t.Fatalf("damage wire = %#x", got)
	}
	if got := DamageMonster(9, -5); int32(binary.LittleEndian.Uint32(got[len(got)-4:])) != -5 {
		t.Fatal("a heal keeps its sign")
	}
}

func TestModePacketCarriesTheCap(t *testing.T) {
	packet := LongDamageModePacket(LongDamageMaxCap)
	if binary.LittleEndian.Uint16(packet) != SendLongDamageMode || int64(binary.LittleEndian.Uint64(packet[2:])) != LongDamageMaxCap {
		t.Fatalf("packet = %x", packet)
	}
}
