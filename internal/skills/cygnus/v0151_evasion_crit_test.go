package cygnus

import (
	"maple-go-server/internal/skills"
	"testing"
	"time"
)

func TestWindEvasionDodgeCriticalRate(t *testing.T) {
	h, ok := skills.Lookup(13110008)
	if !ok || h.Passive == nil || h.OnDamaged == nil {
		t.Fatalf("13110008 Wind Evasion handler incomplete: %+v", h)
	}
	ch := h.OnDamaged(&skills.TriggerContext{
		Values: map[string]int{"er": 30, "time": 1, "x": 1},
		Damage: 0,
		Mob:    88,
		Now:    time.Unix(100, 0),
	})
	if ch == nil || ch.Buff == nil || ch.Buff.Duration != time.Second || len(ch.Buff.Stats) != 1 || ch.Buff.Stats[0].Stat != "CRITICAL_RATE" || ch.Buff.Stats[0].Value != 100 {
		t.Fatalf("13110008 dodge change = %+v, want CRITICAL_RATE=100 for 1s", ch)
	}
	if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"time": 1}, Damage: 25, Mob: 88}); got != nil {
		t.Fatalf("13110008 non-dodge hit = %+v, want nil", got)
	}
}
