package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// Real B4917 initial landing, trace 2026-11-06T08:18:02.8049403+08:00.
const recoveryLandingPacket = "47000103f34aad89cd221b15ffa90000000000030015ffd5000000a40100000000000006d2000015ffd700000000000000000000000604000015ffd700000000000500000000000428011100000000000000000015ffa90015ffd700"

func TestMovementRetainsFinalAdjacentAbsolutePair(t *testing.T) {
	raw, err := hex.DecodeString(recoveryLandingPacket)
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParsePlayerMove(NewReader(raw[2:]))
	if err != nil || !q.Pose.Known || !q.Pose.HasPreviousAbsolute || !q.Pose.Interrupted || q.Pose.PreviousX != -235 || q.Pose.PreviousY != 215 || q.Pose.X != -235 || q.Pose.Y != 215 || q.Pose.VelocityX != 0 || q.Pose.VelocityY != 0 || q.Pose.State != 4 {
		t.Fatal("native landing lost its final pair", q, err)
	}
	// Last absolute fragment alone never manufactures a second sample.
	one := EncodeAbsolutePlayerMove(0, 0, 20, 30)[2:]
	q, err = ParsePlayerMove(NewReader(one))
	if err != nil || q.Pose.HasPreviousAbsolute || q.Pose.Interrupted {
		t.Fatal("single sample fabricated a pair", q, err)
	}
}

func TestMovementPosePairCannotCrossInterveningOrTrailingFragments(t *testing.T) {
	base := EncodeAbsolutePlayerMove(0, 0, 20, 30)[2:]
	abs := append([]byte(nil), base[18:36]...)
	for name, barrier := range map[string][]byte{
		"relative": {1, 0, 0, 0, 0, 4, 30, 0},
		"teleport": {3, 20, 0, 30, 0, 1, 0, 4, 30, 0},
		"opaque":   {17, 4, 30, 0},
	} {
		for _, tail := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/intervening", true: "/tail"}[tail], func(t *testing.T) {
				body := append([]byte(nil), base[:17]...)
				body = append(body, 3)
				body = append(body, abs...)
				if tail {
					body = append(body, abs...)
					body = append(body, barrier...)
				} else {
					body = append(body, barrier...)
					body = append(body, abs...)
				}
				body = append(body, base[36:]...)
				q, err := ParsePlayerMove(NewReader(body))
				if err != nil || !q.Pose.Interrupted || q.Pose.HasPreviousAbsolute || tail && q.Pose.Known {
					t.Fatal("unrelated fragment preserved old pair", q, err)
				}
			})
		}
	}
}

func TestMovementRetainsReviewedPoseWithoutReinterpretingOpaqueFH(t *testing.T) {
	raw := EncodeAbsolutePlayerMove(0, 0, 20, 30)[2:]
	// Existingcommand0 fields: body header17,count1,command1,x/y4,vx/vy4,opaque2,offset4,state1.
	raw[23], raw[24] = 5, 0
	raw[27], raw[28] = 99, 0
	q, err := ParsePlayerMove(NewReader(raw))
	if err != nil || !q.Pose.Known || q.Pose.X != 20 || q.Pose.Y != 30 || q.Pose.VelocityX != 5 || q.Pose.State != 4 || q.Pose.HasFoothold || q.Pose.Foothold != 0 {
		t.Fatal(q, err)
	}
	// A trailing relative fragment invalidates current absolute-pose proof;
	// it does not alter the existing final-absolute coordinate contract.
	base := EncodeAbsolutePlayerMove(0, 0, 20, 30)[2:]
	base[17] = 2
	end := 36
	relative := []byte{1, 0, 0, 0, 0, 4, 30, 0}
	body := append([]byte(nil), base[:end]...)
	body = append(body, relative...)
	body = append(body, base[end:]...)
	q, err = ParsePlayerMove(NewReader(body))
	if err != nil || q.Pose.Known || !q.Changed || q.X != 20 {
		t.Fatal("relative tail forged stationary pose", q, err)
	}
}

// Exact B4917 frames retained from native ingress; no header position is used
// as an absolute sample, and command11's opaque byte is not a stance.
const recoveryMarkerPacket = "47000203f34aadb09d971b15ffd70000000000030015ffd700000000000500000000000496000b040015ffd700000000000500000000000468011100000000000000000015ffd70015ffd700"
const recoveryStopPacket = "47000203f34aadc2869c1b4fffd70051000000020054ffd70009000000030000000000045a000053ffd7000000000003000000000004a401110000000000000000004fffd70054ffd700"

func TestMovementReviewedMarkerAndStopExactFrames(t *testing.T) {
	for _, tc := range []struct {
		raw, sha     string
		x, previousX int16
		interrupted  bool
	}{
		{recoveryMarkerPacket, "d08ba5ab56fff5220b2daa485c82ba73625951138d288774364427485e31bd4b", -235, -235, false},
		{recoveryStopPacket, "91268d136057ae2677b1c07c9f04f957979e741b9afe479658f047e91afbe6b2", -173, -172, true},
	} {
		raw, _ := hex.DecodeString(tc.raw)
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != tc.sha {
			t.Fatal("original wire SHA changed")
		}
		r := NewReader(raw[2:])
		q, err := ParsePlayerMove(r)
		if err != nil || r.Remaining() != 0 || !q.Pose.Known || !q.Pose.HasPreviousAbsolute || q.Pose.MarkerOnly || q.Pose.X != tc.x || q.Pose.PreviousX != tc.previousX || q.Pose.Y != 215 || q.Pose.VelocityX != 0 || q.Pose.VelocityY != 0 || q.Pose.State != 4 || q.Pose.Interrupted != tc.interrupted {
			t.Fatal(q, err, r.Remaining())
		}
	}
}

func TestMovementMarkerNeverCreatesPhysicalSample(t *testing.T) {
	base := EncodeAbsolutePlayerMove(0, 0, 20, 30)[2:]
	abs := base[18:36]
	barrier := []byte{1, 0, 0, 0, 0, 4, 30, 0}
	marker := []byte{11, 4}
	for _, tc := range []struct {
		name                           string
		fragments                      [][]byte
		known, pair, only, interrupted bool
	}{
		{"only", [][]byte{marker}, false, false, true, false},
		{"prefix", [][]byte{marker, abs}, true, false, false, false},
		{"suffixOne", [][]byte{abs, marker}, true, false, false, false},
		{"twoMarkers", [][]byte{abs, marker, marker, abs}, true, true, false, false},
		{"suffixPair", [][]byte{abs, abs, marker}, true, true, false, false},
		{"barrier", [][]byte{abs, barrier, marker, abs}, true, false, false, true},
		{"barrierThenPair", [][]byte{barrier, abs, marker, abs}, true, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := append([]byte(nil), base[:17]...)
			b = append(b, byte(len(tc.fragments)))
			for _, f := range tc.fragments {
				b = append(b, f...)
			}
			b = append(b, base[36:]...)
			q, e := ParsePlayerMove(NewReader(b))
			if e != nil || q.Pose.Known != tc.known || q.Pose.HasPreviousAbsolute != tc.pair || q.Pose.MarkerOnly != tc.only || q.Pose.Interrupted != tc.interrupted {
				t.Fatal(q, e)
			}
		})
	}
	// Truncated marker must fail instead of preserving a preceding good pose.
	b := append([]byte(nil), base[:17]...)
	b = append(b, 2)
	b = append(b, abs...)
	b = append(b, 11)
	if _, e := ParsePlayerMove(NewReader(b)); e == nil {
		t.Fatal("truncated marker accepted")
	}
}
