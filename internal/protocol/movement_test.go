package protocol

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestApplyPlayerMoveUpdatesAbsolutePosition(t *testing.T) {
	packet := EncodeAbsolutePlayerMove(100, 200, 1234, -56)
	r := NewReader(packet)
	if op := r.U16(); op != RecvMove {
		t.Fatalf("opcode=%x", op)
	}
	chr := domain.Character{X: 0, Y: 0, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50, Level: 1, Revision: 1}
	move, err := ApplyPlayerMove(r, &chr)
	if err != nil {
		t.Fatal(err)
	}
	if !move.Changed || chr.X != 1234 || chr.Y != -56 {
		t.Fatalf("changed=%v pos=(%d,%d)", move.Changed, chr.X, chr.Y)
	}
	if len(move.Fragments) < 2 || move.Fragments[0] != 1 {
		t.Fatalf("fragments=%v", move.Fragments)
	}
	move, err = ApplyPlayerMove(NewReader(packet[2:]), &chr)
	if err != nil || move.Changed {
		t.Fatalf("idempotent move: changed=%v err=%v", move.Changed, err)
	}
}

func TestApplyPlayerMoveRejectsUnknownCommand(t *testing.T) {
	w := NewWriter(RecvMove)
	w.U8(0)
	w.I32(0)
	w.I32(0)
	w.I16(0)
	w.I16(0)
	w.I32(0)
	w.U8(1)
	w.U8(99) // unknown movement command
	r := NewReader(w.Packet())
	r.U16()
	chr := domain.Character{Level: 1, HP: 1, MaxHP: 1, MP: 1, MaxMP: 1, Revision: 1}
	if _, err := ApplyPlayerMove(r, &chr); err == nil {
		t.Fatal("expected malformed movement")
	}
}
