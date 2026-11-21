package thief

import (
	"maple-go-server/internal/skills"
	"testing"
	"time"
)

func TestShadowEvasionDodgeBoost(t *testing.T) {
	h, ok := skills.Lookup(4330009)
	if !ok || h.Passive == nil || h.OnDamaged == nil {
		t.Fatalf("4330009 Shadow Evasion handler incomplete: %+v", h)
	}

	ch := h.OnDamaged(&skills.TriggerContext{
		Values: map[string]int{"er": 30, "indiePad": 15, "time": 1},
		Damage: 0,
		Mob:    55,
		Now:    time.Unix(100, 0),
	})
	if ch == nil || ch.Buff == nil || ch.Buff.Duration != time.Second || len(ch.Buff.Stats) != 2 ||
		ch.Buff.Stats[0].Stat != "ANGEL_ATK" || ch.Buff.Stats[0].Value != 15 ||
		ch.Buff.Stats[1].Stat != "CRITICAL_RATE" || ch.Buff.Stats[1].Value != 100 {
		t.Fatalf("4330009 Shadow Evasion dodge change = %+v, want ANGEL_ATK=15 + CRITICAL_RATE=100 for 1s", ch)
	}

	if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"indiePad": 15, "time": 1}, Damage: 50, Mob: 55}); got != nil {
		t.Fatalf("4330009 Shadow Evasion on non-dodge hit = %+v, want nil", got)
	}
	if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"indiePad": 15, "time": 1}, Damage: 0, Mob: 0}); got != nil {
		t.Fatalf("4330009 Shadow Evasion with mob=0 = %+v, want nil", got)
	}
}
