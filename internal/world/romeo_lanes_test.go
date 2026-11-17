package world

import "testing"

func TestRomeoLaneRetryAfterPhysicalFall(t *testing.T) {
	lanes := &romeoLabLanes{owners: [4]int64{7}, progress: [4]int{3}}
	lanes.columns[1][0] = 2

	if next, ok := lanes.step(0, 1, 2, 7); !ok || next != 2 {
		t.Fatalf("correct lower portal after a fall: next=%d, allowed=%v", next, ok)
	}
	if next, ok := lanes.step(0, 1, 3, 7); !ok || next != 0 {
		t.Fatalf("wrong lower portal after a fall: next=%d, allowed=%v", next, ok)
	}
	if _, ok := lanes.step(0, 4, 0, 7); ok {
		t.Fatal("an unreached higher portal was accepted")
	}
	if _, ok := lanes.step(0, 1, 2, 8); ok {
		t.Fatal("another character used the assigned lane")
	}
}
