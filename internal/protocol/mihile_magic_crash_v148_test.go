package protocol

import (
	"encoding/hex"
	"testing"
)

func TestV148MihileMagicCrashActualNativeMobTail(t *testing.T) {
	// Unmodified original v148 capture on the isolated QA field. Native6AB232
	// returns4 for51111005: position, mob count/object IDs and action delay.
	wire, err := hex.DecodeString("a30097202e025de40b031415ffd7000142420f00e80300")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadSkillCast(NewReader(wire[2:]))
	if err != nil || got.SkillID != 51111005 || got.Level != 20 || !got.HasPosition || got.X != -235 || got.Y != 215 || !got.HasMobs || len(got.Mobs) != 1 || got.Mobs[0] != 1000002 || got.ActionDelay != 1000 {
		t.Fatalf("original Magic Crash lost targets: %+v, %v", got, err)
	}
}
