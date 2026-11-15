package world

import (
	"maple-go-server/internal/combat"
	"testing"
	"time"
)

func TestPlayerDriftGrowsSinceLastReport(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, c := range []struct {
		movedAt time.Time
		want    int
	}{
		{now, 0},
		{now.Add(-100 * time.Millisecond), 67},
		{now.Add(-3 * time.Second), 335},
		{now.Add(time.Second), 0},
		{time.Time{}, 335},
	} {
		if got := playerDrift(c.movedAt, now); got != c.want {
			t.Errorf("playerDrift(moved %v) = %d, want %d", c.movedAt, got, c.want)
		}
	}
	// 2026-11-15 12:08:29: stored (401,346), attack sent from (467,455) mid-fall.
	if within(401, 346, 467, 455, attackReach(0, combat.Effect{})) {
		t.Fatal("the stored position alone is out of reach")
	}
	if !within(401, 346, 467, 455, attackReach(0, combat.Effect{})+playerDrift(now.Add(-50*time.Millisecond), now)) {
		t.Fatal("50 ms of falling covers the gap")
	}
}
