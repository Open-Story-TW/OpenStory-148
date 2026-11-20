package common_test

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

func TestNewlyRegisteredCommonGMAndMihileIceKnightSkills(t *testing.T) {
	now := time.Unix(1000, 0)

	// 1) Mihile Ice Knight skills (50000097/99/100/103/1105)
	for _, id := range []int{50000097, 50000099, 50000100, 50000103, 50001105} {
		h, ok := skills.Lookup(id)
		if !ok {
			t.Fatalf("Mihile Ice Knight skill %d not registered", id)
		}
		if id == 50001105 && h.Cast == nil {
			t.Fatalf("50001105 missing Cast handler")
		}
		if id != 50001105 && h.Attack == nil {
			t.Fatalf("%d missing Attack handler", id)
		}
	}

	// 2) Fixed-item flying/glide mounts (v148 sub_6AB7A9)
	for _, tc := range []struct {
		id   int
		item int
	}{
		{1203, 1932103},
		{1204, 1932102},
		{20010022, 1932016},
		{30011109, 1932051},
		{30011159, 1932085},
	} {
		h, ok := skills.Lookup(tc.id)
		if !ok || h.Cast == nil {
			t.Fatalf("mount skill %d not registered", tc.id)
		}
		plan, err := h.Cast(&skills.CastContext{
			Skill:  content.Skill{ID: tc.id},
			Level:  1,
			Values: map[string]int{"time": 2100000, "x": 1},
			Caster: domain.Character{Job: tc.id / 10000},
			Now:    now,
		})
		if err != nil || plan.Self == nil || !plan.Toggle || len(plan.Self.Stats) != 1 || plan.Self.Stats[0].Stat != "MONSTER_RIDING" || plan.Self.Stats[0].Value != tc.item {
			t.Fatalf("mount %d: got plan=%+v err=%v, want MONSTER_RIDING=%d", tc.id, plan, err, tc.item)
		}
	}

	// 3) Common 8000xxxx skills (80000023, 80001128, 80001130, 80001140)
	if _, ok := skills.Lookup(80000023); !ok {
		t.Fatal("80000023 not registered")
	}
	for _, id := range []int{80001128, 80001130} {
		if h, ok := skills.Lookup(id); !ok || h.Attack == nil {
			t.Fatalf("attack skill %d not registered", id)
		}
	}
	h80001140, ok := skills.Lookup(80001140)
	if !ok || h80001140.Cast == nil {
		t.Fatal("80001140 Knight's Watch link skill not registered")
	}
	pStance, err := h80001140.Cast(&skills.CastContext{
		Skill:  content.Skill{ID: 80001140},
		Level:  1,
		Values: map[string]int{"time": 90, "prop": 100},
		Now:    now,
	})
	if err != nil || pStance.Self == nil || len(pStance.Self.Stats) != 1 || pStance.Self.Stats[0].Stat != "STANCE" || pStance.Self.Stats[0].Value != 100 {
		t.Fatalf("80001140: got plan=%+v err=%v", pStance, err)
	}

	// 4) GM skills 8001000, 9001005, 9001009 enforce gmOnly
	for _, id := range []int{8001000, 9001005, 9001009} {
		h, ok := skills.Lookup(id)
		if !ok || h.Cast == nil {
			t.Fatalf("GM skill %d not registered", id)
		}
		if _, err := h.Cast(&skills.CastContext{Skill: content.Skill{ID: id}, Level: 1, Caster: domain.Character{GM: 0}, Now: now}); err == nil {
			t.Fatalf("GM skill %d allowed non-GM caster", id)
		}
		if _, err := h.Cast(&skills.CastContext{Skill: content.Skill{ID: id}, Level: 1, Caster: domain.Character{GM: 1}, Now: now}); err != nil {
			t.Fatalf("GM skill %d rejected GM caster: %v", id, err)
		}
	}

	// 5) Wild Hunter 33101005 Swallow registered
	if h, ok := skills.Lookup(33101005); !ok || h.Cast == nil {
		t.Fatal("33101005 Swallow not registered")
	}
}
