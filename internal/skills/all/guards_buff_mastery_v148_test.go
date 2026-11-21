package all_test

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

func TestBuffMasteryDurationExtension(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, tc := range []struct {
		job, skill int
	}{
		{212, 2121009},
		{222, 2221009},
		{232, 2321010},
	} {
		ch := domain.Character{
			Job:    tc.job,
			Level:  200,
			Skills: []domain.SkillState{{ID: tc.skill, Level: 10}},
		}
		if got := skills.Bonuses(ch, now).BuffDurationPercent; got != 50 {
			t.Fatalf("skill %d BuffDurationPercent = %d, want 50", tc.skill, got)
		}
		b := &skills.Buff{Duration: 600 * time.Second}
		skills.ScaleBuffDuration(ch, b, now)
		if b.Duration != 900*time.Second {
			t.Fatalf("skill %d scaled 600s -> %v, want 900s", tc.skill, b.Duration)
		}
		perm := &skills.Buff{Duration: skills.Permanent}
		skills.ScaleBuffDuration(ch, perm, now)
		if perm.Duration != skills.Permanent {
			t.Fatalf("skill %d altered permanent duration to %v", tc.skill, perm.Duration)
		}
	}

	// Cross-job isolation: learning all three Buff Masteries must only apply the
	// skill belonging to the character's active 4th-job branch, and never leak to
	// non-4th-job-mage branches.
	allThree := []domain.SkillState{
		{ID: 2121009, Level: 10},
		{ID: 2221009, Level: 10},
		{ID: 2321010, Level: 10},
	}
	fp := domain.Character{Job: 212, Level: 200, Skills: allThree}
	if got := skills.Bonuses(fp, now).BuffDurationPercent; got != 50 {
		t.Fatalf("job 212 with all 3 Buff Masteries: BuffDurationPercent = %d, want 50", got)
	}
	for _, otherJob := range []int{211, 2218, 3212, 3311} {
		other := domain.Character{Job: otherJob, Level: 200, Skills: allThree}
		if got := skills.Bonuses(other, now).BuffDurationPercent; got != 0 {
			t.Fatalf("job %d with mage Buff Masteries: BuffDurationPercent = %d, want 0", otherJob, got)
		}
	}
}

func TestMesoGuardTakenDamageWithMesos(t *testing.T) {
	now := time.Unix(1000, 0)
	later := now.Add(time.Minute)

	c50 := domain.Character{
		HP: 5000, MaxHP: 5000, Mesos: 1000,
		SkillBuffs: []domain.SkillBuff{{ID: 4201011, Stat: "MESOGUARD", Value: 50, ExpiresAt: later}},
	}
	if hp, mp, mesos := skills.TakenDamageWithMesos(c50, 1000, now); hp != 500 || mp != 0 || mesos != 250 {
		t.Fatalf("4201011 (50%%): got hp=%d mp=%d mesos=%d, want 500/0/250", hp, mp, mesos)
	}

	c61 := domain.Character{
		HP: 5000, MaxHP: 5000, Mesos: 1000,
		SkillBuffs: []domain.SkillBuff{{ID: 4211005, Stat: "MESOGUARD", Value: 61, ExpiresAt: later}},
	}
	if hp, mp, mesos := skills.TakenDamageWithMesos(c61, 1000, now); hp != 500 || mp != 0 || mesos != 305 {
		t.Fatalf("4211005 (61%%): got hp=%d mp=%d mesos=%d, want 500/0/305", hp, mp, mesos)
	}

	// Low mesos clamps absorption proportionally.
	cLow := c50
	cLow.Mesos = 100
	if hp, mp, mesos := skills.TakenDamageWithMesos(cLow, 1000, now); hp != 800 || mp != 0 || mesos != 100 {
		t.Fatalf("low mesos clamp: got hp=%d mp=%d mesos=%d, want 800/0/100", hp, mp, mesos)
	}

	// Zero mesos absorbs nothing.
	cZero := c50
	cZero.Mesos = 0
	if hp, mp, mesos := skills.TakenDamageWithMesos(cZero, 1000, now); hp != 1000 || mp != 0 || mesos != 0 {
		t.Fatalf("zero mesos: got hp=%d mp=%d mesos=%d, want 1000/0/0", hp, mp, mesos)
	}
}

func TestPowerGuardReductionAndReflect(t *testing.T) {
	now := time.Unix(1000, 0)
	later := now.Add(time.Minute)

	for _, tc := range []struct {
		id, job, pgVal, wantHP int
		mobMaxHP               int64
		wantReflect            int
	}{
		{1101007, 112, 30, 700, 50_000, 1500},
		{1201007, 122, 50, 500, 50_000, 500},
		{11101006, 1110, 30, 700, 5_000, 500}, // capped at MaxHP/10
	} {
		ch := domain.Character{
			Job: tc.job, Level: 200, HP: 5000, MaxHP: 5000,
			Skills:     []domain.SkillState{{ID: tc.id, Level: 20}},
			SkillBuffs: []domain.SkillBuff{{ID: tc.id, Level: 20, Stat: "POWERGUARD", Value: tc.pgVal, ExpiresAt: later}},
		}
		if hp, mp, mesos := skills.TakenDamageWithMesos(ch, 1000, now); hp != tc.wantHP || mp != 0 || mesos != 0 {
			t.Fatalf("skill %d reduction: got hp=%d mp=%d mesos=%d, want %d/0/0", tc.id, hp, mp, mesos, tc.wantHP)
		}
		mobs := []skills.TriggerMob{{ObjectID: 42, MaxHP: tc.mobMaxHP}}
		_, hits := skills.Triggers(ch, now, nil, 1000, 42, mobs, 0)
		if len(hits) != 1 || hits[0].SkillID != tc.id || len(hits[0].Hits) != 1 || hits[0].Hits[0].Damage != tc.wantReflect {
			t.Fatalf("skill %d reflect: got %+v, want damage %d on mob 42", tc.id, hits, tc.wantReflect)
		}
	}
}
