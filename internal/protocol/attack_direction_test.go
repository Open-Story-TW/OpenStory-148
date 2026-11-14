package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

func TestAttackFacingNativeR5LeftWireAndPeerHeaderUnchanged(t *testing.T) {
	// Genuine R5 seq8/tick485880369: original Power Strike facing left,
	// action13; local critical520 has an unmarked damage word. Direction
	// comes from the original OnAttack decoder, independently of criticality.
	const full = "550001112c460f0000573f0488000d8051955cc9020631f2f51c0000000041420f0007810401b4ffd700b4ffd700c20108020000eb016714352be0d9fcffd700"
	wire, err := v148Wire(RecvMeleeAttack, full), error(nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadAttack(RecvMeleeAttack, NewReader(wire[2:]))
	if err != nil || !a.FacingLeft() || a.Display != 0x800D || a.Display&0x7FFF != 13 || a.Tick != 485880369 || !bytes.Equal(a.Wire, wire[2:]) {
		t.Fatalf("native left decode %+v: %v", a, err)
	}
	peer := RemoteAttackPacket(domain.Character{ID: 4910, Level: 150}, RemoteAttack{Opcode: SendMeleeAttack, SkillLevel: 20, Attack: a})
	// Actual full server 0x11D from that same R5 cast. No wire/critical
	// normalization is introduced by correcting the interpreted direction.
	want, _ := hex.DecodeString("34012e1300001196142c460f00000d8006000000000041420f000708020000")
	if !bytes.Equal(peer, want) {
		t.Fatalf("peer bytes changed: %x != %x", peer, want)
	}
}

func TestAttackFacingSharedRemoteHeaderSyntheticBothDirections(t *testing.T) {
	// Synthetic right/left counterparts, not additional live captures.
	// Native 8AC8E6 routes all four opcodes through the same 8BA13E
	// Decode2 / bit15 -> Avatar MoveAction bit0 before family dispatch.
	for _, op := range []uint16{SendMeleeAttack, SendShootAttack, SendMagicAttack, SendBodyAttack} {
		for _, display := range []uint16{0x000D, 0x800D, 0x000E, 0x800E} {
			a := Attack{SkillID: 1001004, Display: display, TByte: 0x11, Targets: []AttackTarget{{ObjectID: 1, Lines: []AttackLine{{Raw: 20, Damage: 20}}}}}
			packet := RemoteAttackPacket(domain.Character{ID: 4910, Level: 150}, RemoteAttack{Opcode: op, SkillLevel: 20, Attack: a})
			r := NewReader(packet)
			if r.U16() != op || r.I32() != 4910 || r.U8() != 0x11 || r.U8() != 150 || r.U8() != 20 || r.I32() != 1001004 || r.U8() != 0 || r.U16() != display {
				t.Fatalf("synthetic shared header opcode%x display%x: %x", op, display, packet)
			}
			if a.FacingLeft() != (display&0x8000 != 0) || a.Display&0x7FFF != display&0x7FFF {
				t.Fatalf("direction/action changed opcode%x display%x", op, display)
			}
		}
	}
}
