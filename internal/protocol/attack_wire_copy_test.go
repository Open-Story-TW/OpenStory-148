package protocol

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestLegacyMeleeWireOwnsExactBodyForAllSupportedFamilies(t *testing.T) {
	for _, skill := range []int{0, 1001004, 1001005} {
		body := physicalMeleeBody(skill, 1)
		want := append([]byte(nil), body...)
		// Reader can begin after an opcode/prefix; the retained body must not.
		packet := append([]byte{0x55, 0}, body...)
		r := NewReader(packet)
		r.U16()
		a, err := ReadMeleeAttack(r)
		if err != nil || !bytes.Equal(a.Wire, want) || a.Tick != 99 {
			t.Fatalf("skill%d exact body/tick lost: %+v err=%v", skill, a, err)
		}
		packet[2] ^= 0xff
		if sha256.Sum256(a.Wire) != sha256.Sum256(want) {
			t.Fatal("retained native body aliases the receive buffer")
		}
		a.Wire[0] ^= 0xff
		if !bytes.Equal(body, want) {
			t.Fatal("retained wire aliases another caller's body")
		}
	}
}

func TestSummonWireOwnsEntireReviewedBodyIncludingOpaqueTail(t *testing.T) {
	body := v148Summon(nativeSummonAttack)
	body = append(body, 0xa5, 0x5a) // Preserve tolerated bytes; do not change parsing.
	want := append([]byte(nil), body...)
	packet := append([]byte{byte(RecvSummonAttack & 255), byte(RecvSummonAttack >> 8)}, body...)
	r := NewReader(packet)
	r.U16()
	in, err := ReadSummonAttack(r)
	if err != nil || !bytes.Equal(in.Wire, want) {
		t.Fatalf("summon original body not retained: %x err=%v", in.Wire, err)
	}
	packet[2] ^= 0xff
	if sha256.Sum256(in.Wire) != sha256.Sum256(want) {
		t.Fatal("summon wire aliases its receive buffer")
	}
}

func TestLegacyLineEvidenceDoesNotEnableCriticalI32Input(t *testing.T) {
	for _, skill := range []int{0, 1001004, 1001005} {
		body := physicalMeleeBody(skill, 1)
		body[57] |= 0x80 // The reviewed legacy I32 damage remains negative (v148 offset).
		if _, err := ReadMeleeAttack(NewReader(body)); err == nil {
			t.Fatalf("skill%d acquired unsupported critical-bit input", skill)
		}
	}
}
