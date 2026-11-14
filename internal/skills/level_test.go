package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestEffectiveLevelFollowsCombatOrders(t *testing.T) {
	now := time.Now()
	c := domain.Character{Job: 122, Skills: []domain.SkillState{{ID: 1221000, Level: 30}, {ID: 1201004, Level: 20}, {ID: 1210001, Level: 10}, {ID: 1000, Level: 1}},
		SkillBuffs: []domain.SkillBuff{{ID: 1211011, Level: 20, Stat: "COMBAT_ORDERS", Value: 2, ExpiresAt: now.Add(time.Minute)}}}
	for _, tc := range []struct{ id, want int }{
		{1221000, 32}, // combatOrders: MaxLevel 30 + 2.
		{1201004, 20}, // No combatOrders: capped at MaxLevel.
		{1210001, 12}, // Below MaxLevel: boosted like Java getTotalSkillLevel.
		{1000, 1},     // Beginner skills never take the bonus.
		{1221002, 0},  // Not learned.
	} {
		if got := EffectiveLevel(c, tc.id, now); got != tc.want {
			t.Fatalf("skill %d: level %d want %d", tc.id, got, tc.want)
		}
	}
	for _, tc := range []struct {
		client, want int
		ok           bool
	}{{32, 32, true}, {30, 30, true}, {31, 0, false}, {33, 0, false}} {
		if got, ok := CastLevel(c, 1221000, 1221000, tc.client, now); got != tc.want || ok != tc.ok {
			t.Fatalf("client %d: %d %v", tc.client, got, ok)
		}
	}
	def, err := content.SkillDefinition("", 1221000)
	if err != nil || def.TrueMax() != 32 {
		t.Fatal("1221000 TrueMax", def.TrueMax(), err)
	}
	if _, err = def.Values(32); err != nil {
		t.Fatal("formula past MaxLevel", err)
	}
	if _, err = def.Values(33); err == nil {
		t.Fatal("level past TrueMax accepted")
	}
	c.SkillBuffs[0].ExpiresAt = now.Add(-time.Second)
	if got := EffectiveLevel(c, 1221000, now); got != 30 {
		t.Fatal("expired COMBAT_ORDERS still boosts", got)
	}
}

func TestValuesPastMaxLevelUsesLastTableRow(t *testing.T) {
	s := content.Skill{ID: 1, MaxLevel: 2, CombatOrders: true, Levels: map[int]map[string]string{1: {"x": "5"}, 2: {"x": "9"}}}
	v, err := s.Values(4)
	if err != nil || v["x"] != 9 {
		t.Fatal(v, err)
	}
}
