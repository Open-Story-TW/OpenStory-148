package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
)

func frameWithDisplay(packet []byte, nonce uint64, count byte, firstByte byte) []byte {
	digest := sha256.Sum256(packet)
	trailer := make([]byte, criticalDisplayTrailer)
	copy(trailer[:4], "OMCD")
	binary.LittleEndian.PutUint64(trailer[4:12], nonce)
	copy(trailer[12:44], digest[:])
	trailer[44], trailer[45], trailer[48] = criticalDisplayVersion, count, firstByte
	copy(trailer[80:], "CRIT")
	return append(append([]byte(nil), packet...), trailer...)
}

func TestCriticalDisplayOnlySetsPeerColor(t *testing.T) {
	const challenge = 17
	original := []byte{byte(RecvMeleeAttack), 0, 0, 0x13, 0, 0, 0, 0}
	packet := frameWithDisplay(original, challenge, 3, 0b010)
	base, display := SplitCriticalDisplay(packet, challenge)
	if !bytes.Equal(base, original) {
		t.Fatal("the original attack changed")
	}
	lines := []AttackLine{{Raw: 777, Damage: 777}, {Raw: 800, Damage: 800}, {Raw: 0x80000323, Damage: 803}}
	targets := []AttackTarget{{ObjectID: 100, Lines: lines}}
	display.Apply(targets, challenge)
	lines[2].InferCriticalDisplay() // Valid metadata overrides the unnegotiated fallback.
	for i, raw := range []uint32{777, 800, 0x80000323} {
		if lines[i].Raw != raw || lines[i].Claimed() != int(raw&0x7fffffff) {
			t.Fatalf("native claim at %d changed: %+v", i, lines[i])
		}
	}
	attack := Attack{Opcode: RecvMeleeAttack, TByte: 0x13, Targets: targets}
	peer := RemoteAttackPacket(domain.Character{ID: 10, Level: 20}, RemoteAttack{Opcode: SendMeleeAttack, Attack: attack})
	want := []uint32{777, 0x80000320, 803}
	for index, expected := range want {
		at := len(peer) - 4*(len(want)-index)
		if binary.LittleEndian.Uint32(peer[at:]) != expected {
			t.Fatalf("foreign display line %d = %x, want %x", index, peer[at:at+4], expected)
		}
	}
}

func TestCriticalDisplayPreservesNativeNumbersAndTargetOrder(t *testing.T) {
	const challenge = 23
	body := []byte{byte(RecvMeleeAttack), 0, 0, 0x22, 0, 0, 0, 0}
	_, hint := SplitCriticalDisplay(frameWithDisplay(body, challenge, 4, 0b0110), challenge)
	mobs := []AttackTarget{
		{ObjectID: 100, Lines: []AttackLine{{Raw: 500, Damage: 499}, {Raw: 120, Damage: 120}}},
		{ObjectID: 101, Lines: []AttackLine{{Raw: 150, Damage: 150}, {Raw: 0, Damage: 0}}},
	}
	hint.Apply(mobs, challenge)
	if mobs[0].Lines[0].displayIsCritical() || !mobs[0].Lines[1].displayIsCritical() ||
		!mobs[1].Lines[0].displayIsCritical() || mobs[1].Lines[1].displayIsCritical() {
		t.Fatal("colored lines did not follow the native target and line order")
	}
	if mobs[0].Lines[0].Claimed() != 500 || mobs[0].Lines[0].Damage != 499 || mobs[0].Lines[1].Critical() {
		t.Fatal("presentation altered a settlement input")
	}
	line := AttackLine{Raw: 130, Damage: 120}
	line.InferCriticalDisplay()
	if line.Raw != 130 || line.Damage != 120 || !line.displayIsCritical() || line.Critical() {
		t.Fatal("unnegotiated color inference changed native or settled values")
	}
}

func TestCriticalDisplayRejectsStaleUnnegotiatedAndMalformedMetadata(t *testing.T) {
	const challenge = 19
	original := []byte{byte(RecvMagicAttack), 0, 0, 0x11, 1, 2, 3, 4}
	valid := frameWithDisplay(original, challenge, 1, 1)
	staleBody := append([]byte(nil), valid...)
	staleBody[4] = 17
	cases := []struct {
		name          string
		wire          []byte
		nonce         uint64
		applyNonce    uint64
		stripMetadata bool
	}{
		{"old session", valid, 0, 0, false},
		{"wrong scene", valid, challenge + 1, challenge + 1, true},
		{"scene changed after decode", valid, challenge, challenge + 1, true},
		{"copied to a different attack", staleBody, challenge, challenge, true},
		{"bitmap bits beyond the declared lines", frameWithDisplay(original, challenge, 1, 0b11), challenge, challenge, true},
		{"line count differs from the native target array", frameWithDisplay(original, challenge, 2, 0b11), challenge, challenge, true},
		{"plain attack", original, challenge, challenge, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, display := SplitCriticalDisplay(tc.wire, tc.nonce)
			expected := tc.wire
			if tc.stripMetadata {
				expected = tc.wire[:len(original)]
			}
			if !bytes.Equal(base, expected) {
				t.Fatal("original packet bytes not preserved")
			}
			lines := []AttackLine{{Raw: 10, Damage: 10}}
			display.Apply([]AttackTarget{{ObjectID: 1, Lines: lines}}, tc.applyNonce)
			if lines[0].visualCriticality != displayUnspecified {
				t.Fatal("invalid critical presentation was accepted")
			}
		})
	}
}

func TestCriticalDisplayCoexistsWithLongDamageTrailer(t *testing.T) {
	old := LongDamageCap()
	t.Cleanup(func() { SetLongDamageCap(old) })
	SetLongDamageCap(LongDamageMaxCap)
	const challenge, alias, damage = 21, 0x7f000007, 2_802_414_308
	original := []byte{byte(RecvShootAttack), 0, 0, 0x11, 1, 2, 3, 4}
	wire := append(frameWithDisplay(original, challenge, 1, 1), longTrailer([2]int64{alias, damage})...)
	var ledger LongDamageLedger
	originalAgain, display := SplitCriticalDisplay(ledger.Absorb(wire), challenge)
	if !bytes.Equal(originalAgain, original) {
		t.Fatal("combined trailers changed the original bytes")
	}
	targets := []AttackTarget{{ObjectID: 1, Lines: []AttackLine{{Raw: alias}}}}
	display.Apply(targets, challenge)
	ledger.Apply(targets)
	got := targets[0].Lines[0]
	if got.Raw != alias || got.Claimed() != damage || !got.displayIsCritical() || got.Critical() {
		t.Fatalf("presentation affected the native long alias or its claim: %+v", got)
	}
}
