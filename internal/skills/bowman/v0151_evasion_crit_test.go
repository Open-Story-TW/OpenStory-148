package bowman

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"testing"
	"time"
)

func TestEvasionBoostDodgeCriticalRate(t *testing.T) {
	now := time.Unix(100, 0)
	for _, id := range []int{3110007, 3210007} {
		h, ok := skills.Lookup(id)
		if !ok || h.Passive == nil || h.OnDamaged == nil {
			t.Fatalf("%d Evasion Boost handler incomplete: %+v", id, h)
		}
		ch := h.OnDamaged(&skills.TriggerContext{
			Values: map[string]int{"er": 30, "time": 1, "x": 1},
			Damage: 0,
			Mob:    42,
			Now:    now,
		})
		if ch == nil || ch.Buff == nil || ch.Buff.Duration != time.Second || len(ch.Buff.Stats) != 1 || ch.Buff.Stats[0].Stat != "CRITICAL_RATE" || ch.Buff.Stats[0].Value != 100 {
			t.Fatalf("%d dodge change = %+v, want CRITICAL_RATE=100 for 1s", id, ch)
		}
		if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"time": 1}, Damage: 10, Mob: 42}); got != nil {
			t.Fatalf("%d non-dodge hit = %+v, want nil", id, got)
		}
		if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"time": 1}, Damage: 0, Mob: 0}); got != nil {
			t.Fatalf("%d mob=0 = %+v, want nil", id, got)
		}
	}
	// Verify CRITICAL_RATE flows into skills.BuffBonus.
	c := domain.Character{
		SkillBuffs: []domain.SkillBuff{{ID: 3110007, Stat: "CRITICAL_RATE", Value: 100, ExpiresAt: now.Add(time.Second)}},
	}
	b, _ := skills.BuffBonus(c, now)
	if b.Critical != 100 {
		t.Fatalf("BuffBonus CRITICAL_RATE = %d, want 100", b.Critical)
	}
}
