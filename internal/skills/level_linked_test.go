package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestLinkedLevelCapsAtTheAttackSkill(t *testing.T) {
	now := time.Now()
	c := domain.Character{Job: 122, Skills: []domain.SkillState{{ID: 1221000, Level: 30}, {ID: 1210001, Level: 10}}}
	if got := LinkedLevel(c, 1221000, 1201004, now); got != 20 {
		t.Fatalf("linked cap without orders: %d", got)
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 1211011, Level: 20, Stat: "COMBAT_ORDERS", Value: 2, ExpiresAt: now.Add(time.Minute)}}
	for _, tc := range []struct{ learned, id, want int }{
		{1221000, 1221000, 32}, // Same as EffectiveLevel.
		{1221000, 1201004, 20}, // The attack skill's TrueMax caps the parent level.
		{1210001, 1221000, 12}, // Orders add to the parent's learned level.
		{1201004, 1201004, 0},  // Not learned.
	} {
		if got := LinkedLevel(c, tc.learned, tc.id, now); got != tc.want {
			t.Fatalf("%d<-%d: %d want %d", tc.id, tc.learned, got, tc.want)
		}
	}
}
