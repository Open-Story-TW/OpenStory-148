package skills

import (
	"maple-go-server/internal/domain"

	"os"
	"testing"
	"time"
)

func TestReviewedPeriodicRecoverySourcePolicy(t *testing.T) {
	if os.Getenv("MAPLEGO_WZ_ROOT") == "" {
		t.Skip("original WZ required")
	}
	now := time.Now()
	for _, tc := range []struct{ id, job, hp, mp int }{
		{1110000, 111, 110, 71}, {1110000, 112, 110, 71},
		{11110000, 1111, 110, 71}, {11110000, 1112, 110, 71},
		{51110000, 5111, 210, 71}, {51110000, 5112, 210, 71},
	} {
		c := domain.Character{MapID: 40000, Job: tc.job, Level: 120, HP: 1, MP: 1, MaxHP: 1000, MaxMP: 1000, INT: 80, Skills: []domain.SkillState{{ID: tc.id, Level: 20}}}
		plans, err := ReviewedRecoveryPlans(c, now)
		if err != nil || len(plans) != 1 || plans[0].HP != tc.hp || plans[0].MP != tc.mp || plans[0].Interval != 4*time.Second {
			t.Fatal(tc, plans, err)
		}
		c.Skills[0].ExpiresAt = now
		if p, err := ReviewedRecoveryPlans(c, now); err != nil || len(p) != 0 {
			t.Fatal("expired source ran", p, err)
		}
		c.Skills[0].ExpiresAt = time.Time{}
		c.Job = 3112
		if p, err := ReviewedRecoveryPlans(c, now); err != nil || len(p) != 0 {
			t.Fatal("foreign source recovered Demon Fury", p, err)
		}
	}
}

func TestReviewedPeriodicRecoveryOriginalWZMapRates(t *testing.T) {
	if os.Getenv("MAPLEGO_WZ_ROOT") == "" {
		t.Skip("original WZ required")
	}
	for _, tc := range []struct{ mapID, job, id, hp, mp int }{{105000011, 111, 1110000, 40, 34}, {105000012, 111, 1110000, 60, 51}, {105000011, 1111, 11110000, 40, 34}, {105000012, 1111, 11110000, 60, 51}} {
		c := domain.Character{MapID: tc.mapID, Job: tc.job, Level: 120, HP: 1, MP: 1, MaxHP: 1000, MaxMP: 1000, INT: 80, Skills: []domain.SkillState{{ID: tc.id, Level: 2}}}
		plans, err := ReviewedRecoveryPlans(c, time.Now())
		if err != nil || len(plans) != 1 || plans[0].HP != tc.hp || plans[0].MP != tc.mp {
			t.Fatal(tc, plans, err)
		}
	}
}

func TestReviewedBloodCirculationOriginalWZ(t *testing.T) {
	if os.Getenv("MAPLEGO_WZ_ROOT") == "" {
		t.Skip("original WZ required")
	}
	now := time.Unix(1000, 0)
	for _, job := range []int{570, 571, 572} {
		c := domain.Character{
			MapID: 40000, Job: job, Level: 120,
			HP: 1025, MP: 63, MaxHP: 7193, MaxMP: 84,
			Skills: []domain.SkillState{{ID: bloodCirculationSkill, Level: 15}},
		}
		want := PeriodicRecoveryPlan{SkillID: bloodCirculationSkill, HP: 575, MP: 6, Interval: 5 * time.Second}
		if plans, err := ReviewedRecoveryPlans(c, now); err != nil || len(plans) != 1 || plans[0] != want {
			t.Fatalf("job %d: plans %v, want %v (%v)", job, plans, want, err)
		}
		c.Job = 510
		if plans, err := ReviewedRecoveryPlans(c, now); err != nil || len(plans) != 0 {
			t.Fatalf("foreign job applied Blood Circulation: %v (%v)", plans, err)
		}
	}
}
