package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestPotentialNativeInputExactShapeAndSignedSlots(t *testing.T) {
	// Native 0x916AA8: tickDWORD, sourceWORD, targetWORD. Target is I16;
	// trait127 is a distinct source path whose authorization is not decoded here.
	body, _ := hex.DecodeString("785634127f00f5ff")
	in, err := ReadMagnifyInput(NewReader(body))
	if err != nil || in.ClientTick != 0x12345678 || in.SourceSlot != 127 || in.TargetSlot != -11 {
		t.Fatal("native magnifier shape changed", in, err)
	}
	for n := 0; n < len(body); n++ {
		if _, err := ReadMagnifyInput(NewReader(body[:n])); err == nil {
			t.Fatal("truncated magnifier frame accepted", n)
		}
	}
	if _, err := ReadMagnifyInput(NewReader(append(body, 0))); err == nil {
		t.Fatal("unproven magnifier tail accepted")
	}

	// Potential scroll has no ordinary-scroll white flags word.
	potential := append([]byte(nil), body...)
	potential = append(potential, 1)
	scroll, err := ReadPotentialScrollInput(NewReader(potential))
	if err != nil || scroll.ClientTick != in.ClientTick || scroll.SourceSlot != 127 || scroll.TargetSlot != -11 || !scroll.LegendarySpirit {
		t.Fatal("potential scroll differs from original sender", scroll, err)
	}
	for n := 0; n < len(potential); n++ {
		if _, err := ReadPotentialScrollInput(NewReader(potential[:n])); err == nil {
			t.Fatal("truncated potential scroll accepted", n)
		}
	}
	for _, bad := range [][]byte{append(append([]byte(nil), body...), 2), append(potential, 0, 0)} {
		if _, err := ReadPotentialScrollInput(NewReader(bad)); err == nil {
			t.Fatal("noncanonical potential scroll accepted")
		}
	}
}

func TestMagnifySuccessMatchesOriginalLocalUIDecoder(t *testing.T) {
	// Native CUserPool dispatch0x8AE121 -> CUser0x83DE9B reads CID4+slot2.
	// Unlike scroll0xF0 there is no status, scroll ID or additional trailer.
	want, _ := hex.DecodeString("080104030201f5ff")
	if got := MagnifySuccess(0x01020304, -11); !bytes.Equal(got, want) {
		t.Fatalf("native F2 mismatch got%x want%x", got, want)
	}
	for _, args := range [][2]int64{{0, 1}, {-1, 1}, {0x80000000, 1}, {1, 0}, {1, 32768}, {1, -32769}} {
		if MagnifySuccess(args[0], int(args[1])) != nil {
			t.Fatal("invalid success identity truncated into a valid packet", args)
		}
	}
}
