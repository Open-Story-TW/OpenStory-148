package protocol

import (
	"bytes"
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
		// v148 writes the initial velocity at the start of the path. Set a
		// signed, nonzero velocity in the captured frame before relaying it.
		velocity := []byte{0x96, 0xff, 0x04, 0x00}
		copy(data[39:43], velocity)
		m, err = ReadMonsterMove(NewReader(data[2:]))
		if err != nil || m.StartVX != -106 || m.StartVY != 4 {
			t.Fatalf("path velocity: %+v %v", m, err)
		}
		if sent := MonsterMovePacket(m); !bytes.Equal(sent[18:22], velocity) {
			t.Fatalf("relayed path velocity %x, want %x", sent[18:22], velocity)
		}
	}
}

func TestNettDefenseWalkUsesNativePathWithoutAction(t *testing.T) {
	want, err := hex.DecodeString("ae0143420f0000ff0000000000008e039b0096ff0000010024039b0096ff000010000000000003e803")
	if err != nil {
		t.Fatal(err)
	}
	if got := NettDefenseWalk(1000003, 910, 804, 155, 16, 1000); !bytes.Equal(got, want) {
		t.Fatalf("walk packet = %x, want %x", got, want)
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
