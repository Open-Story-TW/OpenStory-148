package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// Live Monster Bomb 4341003 key-down (T, 2026-11-02): 97 00 | 0B3D4200 1E CE00 04.
func TestSkillPrepare(t *testing.T) {
	b, _ := hex.DecodeString("0b3d42001ece0004")
	p, err := ReadSkillPrepare(NewReader(b))
	if err != nil || p.SkillID != 4341003 || p.Level != 30 || p.Direction != 0xce || p.Speed != 4 {
		t.Fatalf("%+v %v", p, err)
	}
	out := SkillPreparePacket(7, p)
	if !bytes.Equal(out, append([]byte{0x38, 0x01, 7, 0, 0, 0}, b...)) {
		t.Fatalf("% x", out)
	}
	if c := SkillPrepareCancel(7, 4341003); !bytes.Equal(c, []byte{0x3a, 0x01, 7, 0, 0, 0, 0x0b, 0x3d, 0x42, 0}) {
		t.Fatalf("% x", c)
	}
}

// Monster Bomb release (T, 2026-11-02): x -619, y 127, character y 287, held
// 1200ms, skill 4341003, then facing, level and the grenade key.
func TestReadThrowGrenade(t *testing.T) {
	b, _ := hex.DecodeString("95fdffff7f0000001f010000b00400000b3d4200011e00000007000000")
	g, err := ReadThrowGrenade(NewReader(b))
	if err != nil || g.X != -619 || g.Y != 127 || g.CharacterY != 287 || g.HoldMS != 1200 || g.SkillID != 4341003 || !g.FacingLeft || g.Action != 30 || g.Key != 7 {
		t.Fatalf("%+v %v", g, err)
	}
}
