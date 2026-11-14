package protocol

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// Actual 2026-11-07 CID12941, Poison Mist1000062, five Eruption lines.
const nativeMistEruption = "570002152b5d20000000bd8a67002601f00d79a906069b08921e000000007d420f00078003017fffd70082ffd70038045d0a00006b080000a40300009a0700005d0a0000db323b5856b5031115ffd70000017e420f00"

func TestMistEruptionNativeAreaTrailer(t *testing.T) {
	wire := v148Wire(RecvMagicAttack, nativeMistEruption)
	a, err := ReadAttack(RecvMagicAttack, NewReader(wire[2:]))
	if err != nil || a.SkillID != 2121003 || a.TargetCount() != 1 || a.LineCount() != 5 || len(a.EruptionAreas) != 1 || a.EruptionAreas[0] != 1000062 || len(a.Wire) != 88 {
		t.Fatalf("native eruption: %+v %v", a, err)
	}
	if got := a.Targets[0]; got.ObjectID != 1000061 || len(got.Lines) != 5 || got.Lines[0].Raw != 2653 || got.Lines[1].Raw != 2155 || got.Lines[2].Raw != 932 || got.Lines[3].Raw != 1946 || got.Lines[4].Raw != 2653 {
		t.Fatalf("native target changed: %+v", got)
	}
	if hex.EncodeToString(a.Wire) != hex.EncodeToString(wire[2:]) {
		t.Fatal("wire attribution lost the typed trailer")
	}
	full := append([]byte(nil), wire[2:]...)
	full[len(full)-5] = 6
	for id := int32(1000063); id < 1000068; id++ {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(id))
		full = append(full, b...)
	}
	if all, err := ReadAttack(RecvMagicAttack, NewReader(full)); err != nil || len(all.EruptionAreas) != 6 {
		t.Fatalf("six distinct areas: %+v %v", all.EruptionAreas, err)
	}
	for n := range len(wire) - 2 {
		if _, err := ReadAttack(RecvMagicAttack, NewReader(wire[2:2+n])); err == nil {
			t.Fatalf("accepted truncation%d", n)
		}
	}
	for _, name := range []string{"zero", "seven", "duplicate", "negative", "zero_id", "extra", "other_skill", "tail_flag"} {
		t.Run(name, func(t *testing.T) {
			b := append([]byte(nil), wire[2:]...)
			switch name {
			case "zero":
				b[len(b)-5] = 0
				b = b[:len(b)-4]
			case "seven":
				b[len(b)-5] = 7
				b = append(b, make([]byte, 24)...)
			case "duplicate":
				b[len(b)-5] = 2
				b = append(b, b[len(b)-4:]...)
			case "negative":
				binary.LittleEndian.PutUint32(b[len(b)-4:], 0xffffffff)
			case "zero_id":
				binary.LittleEndian.PutUint32(b[len(b)-4:], 0)
			case "extra":
				b = append(b, 0)
			case "other_skill":
				binary.LittleEndian.PutUint32(b[2:6], 2121007)
			case "tail_flag":
				b[len(b)-6] = 1
			}
			if _, err := ReadAttack(RecvMagicAttack, NewReader(b)); err == nil {
				t.Fatal("accepted invalid eruption trailer")
			}
		})
	}
}
