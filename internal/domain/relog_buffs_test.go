package domain

import "testing"

func TestRelogBuffsSwitch(t *testing.T) {
	restore := SetRelogBuffsForTest(true)
	if !RelogBuffsEnabled() {
		t.Fatal("switch on, RelogBuffsEnabled() = false")
	}
	SetRelogBuffsForTest(false)
	if RelogBuffsEnabled() {
		t.Fatal("switch off, RelogBuffsEnabled() = true")
	}
	restore()
}
