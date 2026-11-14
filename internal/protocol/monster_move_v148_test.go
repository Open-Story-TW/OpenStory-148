package protocol

import (
	"encoding/hex"
	"testing"
)

// Live v148 CMob::SendMoveRequest frames (opcode 0x163) captured from a
// snail controller in 100010000.
func TestReadMonsterMoveAcceptsLiveV148Frames(t *testing.T) {
	for _, frame := range []string{
		"630153420f0000010000ff0000000000000001000000ccddff00ccddff00f5d774ea03c005c9ff000000000200c005c9ff0000000000000000000004000000c005c9ff000000003f000000000004380400c005c9ffc005c9ff000faa8ebe5a139a6e3f000000dcdb27dd00000000030000000001",
		"630142420f0000010000ff0000000000000001000000ccddff00ccddff00f5d774ea031f02c7010000000004001f02c70100000000000000000000040000001f02c70100000000a30000000000045a00007602c7017d000000a3000000000002e502009502c7017d00000097000000000002f900001f02c7019502c701000faa8ebeeb54f545970000000087acd800000000030000000001",
	} {
		data, err := hex.DecodeString(frame)
		if err != nil {
			t.Fatal(err)
		}
		r := NewReader(data)
		if r.U16() != RecvMoveMonster {
			t.Fatal("opcode")
		}
		m, err := ReadMonsterMove(r)
		if err != nil {
			t.Fatalf("frame %s: %v", frame[:20], err)
		}
		if m.ObjectID <= 0 || len(m.Fragments) == 0 {
			t.Fatalf("parsed %+v", m)
		}
	}
}

// Live v148 melee frames (job 100, skill 0, no target) from 100010000.
func TestReadMeleeAttackAcceptsLiveV148Frames(t *testing.T) {
	for _, frame := range []string{
		"550001010000000000000000000010803afe99de0102ad61402e000000000000000002fec701",
		"550001010000000000000000000007803afe99de01022364402e000000000000000002fec701",
	} {
		data, _ := hex.DecodeString(frame)
		a, err := ReadAttack(RecvMeleeAttack, NewReader(data[2:]))
		if err != nil || a.SkillID != 0 || a.Speed != 2 || a.Action != 1 || a.X != -510 || a.Y != 455 || !a.FacingLeft() {
			t.Fatalf("generic %s: %+v %v", frame[:24], a, err)
		}
		m, err := ReadMeleeAttack(NewReader(data[2:]))
		if err != nil || m.Skill != 0 || m.X != -510 || m.Y != 455 {
			t.Fatalf("legacy %s: %+v %v", frame[:24], m, err)
		}
	}
}
