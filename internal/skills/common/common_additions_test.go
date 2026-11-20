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

func TestV148RemainingCommonPhantomMihileAndEventRegistrations(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	now := time.Unix(2000, 0)

	// 1) Phantom 20030206 High Dexterity: DEX +40 and AvoidRate +20 from v148 WZ.
	hDex, ok := skills.Lookup(20030206)
	if !ok || hDex.Passive == nil {
		t.Fatal("20030206 High Dexterity not registered")
	}
	defDex, err := content.SkillDefinition("", 20030206)
	if err != nil {
		t.Fatalf("20030206 WZ: %v", err)
	}
	valsDex, err := defDex.Values(1)
	if err != nil {
		t.Fatalf("20030206 values: %v", err)
	}
	b := hDex.Passive(&skills.PassiveContext{Skill: defDex, Level: 1, Values: valsDex, Character: domain.Character{Job: 2003}, Now: now})
	if b.DEX != 40 || b.AvoidRate != 20 {
		t.Fatalf("20030206 bonus=%+v, want DEX=40 AvoidRate=20", b)
	}

	// 2) Phantom 20031211 Sneaky Move: INFILTRATE=1 + SPEED=20 for 10s.
	hSneak, ok := skills.Lookup(20031211)
	if !ok || hSneak.Cast == nil {
		t.Fatal("20031211 not registered")
	}
	defSneak, err := content.SkillDefinition("", 20031211)
	if err != nil {
		t.Fatalf("20031211 WZ: %v", err)
	}
	valsSneak, err := defSneak.Values(1)
	if err != nil {
		t.Fatalf("20031211 values: %v", err)
	}
	pSneak, err := hSneak.Cast(&skills.CastContext{Skill: defSneak, Level: 1, Values: valsSneak, Caster: domain.Character{Job: 2003}, Now: now})
	if err != nil || pSneak.Self == nil || pSneak.Self.Duration != 10*time.Second {
		t.Fatalf("20031211 cast plan=%+v err=%v", pSneak, err)
	}
	gotStats := map[string]int{}
	for _, sv := range pSneak.Self.Stats {
		gotStats[string(sv.Stat)] = sv.Value
	}
	if gotStats["INFILTRATE"] != 1 || gotStats["SPEED"] != 20 {
		t.Fatalf("20031211 stats=%+v, want INFILTRATE=1 SPEED=20", gotStats)
	}

	// 3) Mihile beginner Pharaoh's Wrath (50001020) and Giant Potion attacks (50001092/50001094/50001095).
	for _, id := range []int{50001020, 50001092, 50001094, 50001095} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil {
			t.Fatalf("Mihile beginner attack %d not registered", id)
		}
	}

	// 4) Event stationary puppet summon 80001141.
	hSum, ok := skills.Lookup(80001141)
	if !ok || hSum.Cast == nil || hSum.Attack == nil || hSum.Attack.Summon == nil {
		t.Fatal("80001141 summon not registered")
	}
	defSum, err := content.SkillDefinition("", 80001141)
	if err != nil {
		t.Fatalf("80001141 WZ: %v", err)
	}
	valsSum, err := defSum.Values(1)
	if err != nil {
		t.Fatalf("80001141 values: %v", err)
	}
	pSum, err := hSum.Cast(&skills.CastContext{Skill: defSum, Level: 1, Values: valsSum, Caster: domain.Character{Job: 100}, Now: now})
	if err != nil || pSum.Self == nil || pSum.Self.Duration != 8*time.Second || len(pSum.Self.Stats) != 1 || pSum.Self.Stats[0].Stat != "PUPPET" || pSum.Self.Stats[0].Value != 1 {
		t.Fatalf("80001141 cast plan=%+v err=%v", pSum, err)
	}
}
