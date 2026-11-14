package world

import (
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// A party buff's planned stats must match the rows persistence saved, which
// use the indie form while MAPLEGO_STACK_BUFFS is on (1101006 Rage: WATK).
func TestPlannedBuffStatsFollowStacking(t *testing.T) {
	restore := domain.SetStackBuffsForTest(false)
	defer restore()
	plan := []skills.StatValue{{Stat: "WATK", Value: 30}, {Stat: "WDEF", Value: -10}}
	if got := plannedBuffStats(plan); len(got) != 2 || got["WATK"] != 30 || got["WDEF"] != -10 {
		t.Fatalf("off %v", got)
	}
	domain.SetStackBuffsForTest(true)
	if got := plannedBuffStats(plan); len(got) != 2 || got["ANGEL_ATK"] != 30 || got["WDEF"] != -10 {
		t.Fatalf("on %v", got)
	}
	merged := plannedBuffStats([]skills.StatValue{{Stat: "WATK", Value: 30}, {Stat: "ANGEL_ATK", Value: 5}})
	if len(merged) != 1 || merged["ANGEL_ATK"] != 35 {
		t.Fatalf("merged %v", merged)
	}
}
