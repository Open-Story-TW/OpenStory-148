package common_test

import (
	"testing"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestPrologueTutorialSkills(t *testing.T) {
	for _, id := range []int{20000014, 20000015, 20000016, 30010166, 30011167, 30011168, 30011169, 30011170} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || !h.Attack.Allows(combat.Melee) || h.Attack.Ammo != combat.AmmoNone {
			t.Errorf("melee tutorial skill %d: ok=%v attack=%+v", id, ok, h.Attack)
		}
	}
	for _, id := range []int{30011167, 30011168, 30011169} {
		h, _ := skills.Lookup(id)
		if h.LevelFrom != 30010166 || h.Attack.LevelFrom != 30010166 {
			t.Errorf("demon tutorial follow-up %d: LevelFrom=%d attack.LevelFrom=%d", id, h.LevelFrom, h.Attack.LevelFrom)
		}
	}
	h, _ := skills.Lookup(30011170)
	ctx := &combat.AttackContext{Effect: combat.Effect{SkillID: 30011170}}
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.CriticalRate != 100 {
		t.Fatalf("30011170 prepare: cr=%d err=%v", ctx.Effect.CriticalRate, err)
	}
	for _, id := range []int{20021166, 20031166} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || !h.Attack.Allows(combat.Shoot) || !h.Attack.Allows(combat.Melee) {
			t.Errorf("prologue ranged tutorial skill %d: ok=%v attack=%+v", id, ok, h.Attack)
		}
	}
	for _, id := range []int{20021181, 20031181} {
		p := mustCast(t, id, 1, domain.Character{})
		if p.Self != nil || p.Party != nil || p.MPCost != 0 {
			t.Errorf("prologue jump %d: unexpected plan %+v", id, p)
		}
	}
	for _, id := range []int{20000017, 20000018, 20020111} {
		h, ok := skills.Lookup(id)
		if !ok || h.Kind != skills.KindPassive {
			t.Errorf("prologue passive %d: ok=%v kind=%v", id, ok, h.Kind)
		}
	}
}
