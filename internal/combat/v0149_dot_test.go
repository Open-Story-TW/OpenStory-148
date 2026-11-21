package combat

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
	"time"
)

func TestPlanAttackAppliesDOTTimeAndDOTPercent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	baseStats := PlayerStats{
		Job: 212, Level: 200, INT: 500, LUK: 100, MATK: 500, Mastery: 50,
	}
	boostedStats := baseStats
	boostedStats.DOTTime = 10
	boostedStats.DOTPercent = 10

	spec := &AttackSpec{
		Kind:        Magic,
		IgnoreRange: true,
		OnHit:       []MobStatusApply{{Stat: MobPoison, Value: 1, Poison: true}},
	}
	eff := Effect{
		SkillID:     2101005,
		Damage:      150,
		MobCount:    1,
		AttackCount: 1,
		DOT:         100,
		DOTTime:     10 * time.Second,
		DOTInterval: time.Second,
	}
	planFor := func(st PlayerStats) Plan {
		t.Helper()
		ctx := AttackContext{
			Kind:      Magic,
			Character: domain.Character{ID: 7, Job: 212, Level: 200, MP: 5000, MaxMP: 5000},
			Stats:     st,
			Effect:    eff,
			Now:       now,
			Attack:    attack(protocol.RecvMagicAttack, 2101005, 0, 0, map[int32][]uint32{1: {100}}),
			Targets:   []TargetInfo{{ObjectID: 1, Mob: content.Mob{ID: 9990000, Level: 10, HP: 1_000_000}, HP: 1_000_000}},
		}
		p, err := PlanAttack(spec, ctx, func(int) bool { return true })
		if err != nil {
			t.Fatalf("PlanAttack failed: %v", err)
		}
		if len(p.Targets) != 1 || len(p.Targets[0].Statuses) != 1 {
			t.Fatalf("expected 1 poison status, got %+v", p.Targets)
		}
		return p
	}

	p0 := planFor(baseStats)
	p1 := planFor(boostedStats)
	st0 := p0.Targets[0].Statuses[0]
	st1 := p1.Targets[0].Statuses[0]
	dur0 := st0.ExpiresAt.Sub(now)
	dur1 := st1.ExpiresAt.Sub(now)
	if dur1-dur0 != 10*time.Second {
		t.Fatalf("expected +10s DoT duration from DOTTime=10, got before=%v after=%v", dur0, dur1)
	}
	if st1.TickDamage <= st0.TickDamage {
		t.Fatalf("expected higher DoT tick damage from DOTPercent=10, got before=%d after=%d", st0.TickDamage, st1.TickDamage)
	}
}
