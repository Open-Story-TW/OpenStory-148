package domain

import "testing"

func TestIntLevelCapIsOnUnlessExplicitlyOff(t *testing.T) {
	if got := parseIntLevelCap("0", "5000"); got != 0 {
		t.Fatalf("off switch cap=%d", got)
	}
	for _, enabled := range []string{"", "1", "true"} {
		if got := parseIntLevelCap(enabled, ""); got != defaultIntLevelCap {
			t.Fatalf("enabled=%q cap=%d", enabled, got)
		}
	}
	for maximum, want := range map[string]int{
		"":           defaultIntLevelCap,
		"abc":        defaultIntLevelCap,
		"-1":         defaultIntLevelCap,
		"255":        defaultIntLevelCap,
		"2147483648": defaultIntLevelCap,
		"256":        256,
		"5000":       5000,
		"2147483647": 2147483647,
	} {
		if got := parseIntLevelCap("1", maximum); got != want {
			t.Fatalf("maximum=%q cap=%d want=%d", maximum, got, want)
		}
	}
}

func TestWireLevelByteSaturatesInsteadOfWrapping(t *testing.T) {
	for level, want := range map[int]byte{1: 1, 200: 200, 255: 255, 256: 255, 300: 255, 2147483647: 255} {
		if got := WireLevelByte(level); got != want {
			t.Fatalf("level=%d byte=%d want=%d", level, got, want)
		}
	}
}
