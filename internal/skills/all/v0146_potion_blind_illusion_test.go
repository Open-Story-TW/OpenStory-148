package all_test

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

func TestPotionMasteryV148RecoveryModifiers(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name  string
		job   int
		skill int
		level int
		want  int
	}{
		{"nl_4110014", 412, 4110014, 10, 70},
		{"nw_14110011", 1412, 14110011, 10, 70},
		{"res_30000002", 3212, 30000002, 3, 15},
	} {
		ch := domain.Character{
			Job:    tc.job,
			Skills: []domain.SkillState{{ID: tc.skill, Level: tc.level}},
		}
		mod, err := skills.PotionRecoveryModifiersChecked(ch, now)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if mod.FixedHPPercent != tc.want {
			t.Fatalf("%s: FixedHPPercent=%d, want %d", tc.name, mod.FixedHPPercent, tc.want)
		}
	}
}

func TestIllusionStepDodgeBuffBonus(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		id   int
		stat string
	}{
		{3121007, "HAMSTRING"},
		{3221006, "BLIND"},
	} {
		ch := domain.Character{
			SkillBuffs: []domain.SkillBuff{
				{ID: tc.id, Level: 30, Stat: "DEX", Value: 40, ExpiresAt: now.Add(time.Minute)},
				{ID: tc.id, Level: 30, Stat: tc.stat, Value: 30, ExpiresAt: now.Add(time.Minute)},
			},
		}
		b, _ := skills.BuffBonus(ch, now)
		if b.DEX != 40 || b.AvoidRate != 30 {
			t.Fatalf("skill %d: got DEX=%d AvoidRate=%d, want 40/30", tc.id, b.DEX, b.AvoidRate)
		}
	}
	// Wild Hunter 33111004 BLIND must not add Illusion Step AvoidRate.
	wh := domain.Character{
		SkillBuffs: []domain.SkillBuff{{ID: 33111004, Level: 10, Stat: "BLIND", Value: 20, ExpiresAt: now.Add(time.Minute)}},
	}
	if b, _ := skills.BuffBonus(wh, now); b.AvoidRate != 0 {
		t.Fatalf("wh 33111004 must not add AvoidRate, got %d", b.AvoidRate)
	}
}

func TestWildHunterBlindOnHitMobACC(t *testing.T) {
	now := time.Now()
	h, ok := skills.Lookup(33001000)
	if !ok || h.Attack == nil || h.Attack.Prepare == nil {
		t.Fatalf("33001000 missing Attack.Prepare")
	}
	ctx := &combat.AttackContext{
		Character: domain.Character{
			Job:        3311,
			Skills:     []domain.SkillState{{ID: 33111004, Level: 10}},
			SkillBuffs: []domain.SkillBuff{{ID: 33111004, Level: 10, Stat: "BLIND", Value: 20, ExpiresAt: now.Add(time.Minute)}},
		},
		Now: now,
	}
	if err := h.Attack.Prepare(ctx); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(ctx.Effect.MonsterStatus) != 1 {
		t.Fatalf("expected 1 MobACC status, got %+v", ctx.Effect.MonsterStatus)
	}
	st := ctx.Effect.MonsterStatus[0]
	if st.Stat != combat.MobACC || st.Value != -20 || st.Prop != 30 || st.Duration != 10*time.Second || st.SourceSkill != 33111004 {
		t.Fatalf("unexpected MobACC status: %+v", st)
	}
}
