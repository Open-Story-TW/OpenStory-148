package warrior

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"testing"
	"time"
)

func TestGuardianOnDamagedStun(t *testing.T) {
	h, ok := skills.Lookup(1220006)
	if !ok || h.Passive == nil || h.OnDamaged == nil || h.Attack == nil || h.Attack.MobCast == nil {
		t.Fatalf("1220006 Guardian handler incomplete: %+v", h)
	}
	if len(h.Attack.MobCast.Statuses) != 1 || h.Attack.MobCast.Statuses[0].Stat != combat.MobStun || h.Attack.MobCast.Statuses[0].Prop != 100 {
		t.Fatalf("1220006 Guardian MobCast statuses = %+v, want MobStun prop=100", h.Attack.MobCast.Statuses)
	}

	orig := guardianRoll
	defer func() { guardianRoll = orig }()

	guardianRoll = func(int) bool { return true }
	ch := h.OnDamaged(&skills.TriggerContext{
		Values: map[string]int{"prop": 40, "time": 6},
		Damage: 120,
		Mob:    77,
		Now:    time.Unix(100, 0),
	})
	if ch == nil || len(ch.MobCast) != 1 || ch.MobCast[0] != 77 {
		t.Fatalf("1220006 Guardian on roll hit = %+v, want MobCast=[77]", ch)
	}

	if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"prop": 40}, Damage: 120, Mob: 0}); got != nil {
		t.Fatalf("1220006 Guardian with mob=0 = %+v, want nil", got)
	}
	if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"prop": 40}, Damage: 0, DefType: 0, Mob: 77}); got != nil {
		t.Fatalf("1220006 Guardian with damage=0,defType=0 = %+v, want nil", got)
	}

	guardianRoll = func(int) bool { return false }
	if got := h.OnDamaged(&skills.TriggerContext{Values: map[string]int{"prop": 40}, Damage: 120, Mob: 77}); got != nil {
		t.Fatalf("1220006 Guardian on roll miss = %+v, want nil", got)
	}
}
