package persistence

import (
	"errors"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestReviewedFirstJobQuestGrowthUsesBaseAndPreservesAttributes(t *testing.T) {
	for _, tc := range []struct{ job, level, hp, mp, mpRange int }{
		{100, 10, 48, 4, 3}, {200, 8, 10, 48, 5},
		{300, 10, 20, 14, 3}, {400, 10, 20, 14, 3}, {500, 10, 20, 18, 5},
	} {
		c := domain.Character{Job: tc.job, Level: tc.level, STR: 20, DEX: 25, INT: 20, LUK: 4, AP: 2, SP: 1,
			VitalsModel: 1, BaseMaxHP: 500, BaseMaxMP: 200, MaxHP: 520, MaxMP: 207,
			HP: 10, MP: 5, Equipment: []domain.Equipment{{Slot: -5, HP: 20, MP: 7}}}
		if err := applyQuestExperience(&c, content.ExperienceForLevel(tc.level), "first-job-growth"); err != nil {
			t.Fatalf("job%d: %v", tc.job, err)
		}
		if c.Level != tc.level+1 || c.EXP != 0 || c.BaseMaxHP < 500+tc.hp || c.BaseMaxHP > 504+tc.hp || c.BaseMaxMP < 200+tc.mp || c.BaseMaxMP >= 200+tc.mp+tc.mpRange {
			t.Fatalf("job%d growth: %+v", tc.job, c)
		}
		if c.MaxHP != c.BaseMaxHP+20 || c.MaxMP != c.BaseMaxMP+7 || c.HP != c.MaxHP || c.MP != c.MaxMP || c.SP != 4 || c.AP != 7 || c.STR != 20 || c.DEX != 25 || c.INT != 20 || c.LUK != 4 {
			t.Fatalf("job%d changed intrinsic stats or lost equipment derivation: %+v", tc.job, c)
		}
	}
}

func TestNonexistentOrPrematureJobsCannotEarnQuestGrowth(t *testing.T) {
	for _, tc := range []struct{ job, level int }{{100, 9}, {200, 7}, {300, 9}, {400, 9}, {500, 9}, {133, 30}, {600, 30}, {800, 10}, {900, 10}} {
		c := domain.Character{Job: tc.job, Level: tc.level, EXP: 0, MaxHP: 50, MaxMP: 5}
		if err := applyQuestExperience(&c, 1, "unsupported-job"); !errors.Is(err, domain.ErrInvalidInput) || c.EXP != 0 || c.Level != tc.level {
			t.Fatalf("job%d level%d accepted/partially changed: %+v %v", tc.job, tc.level, c, err)
		}
	}
}

func TestEveryFamilyLevelsWithLegacyGrowth(t *testing.T) {
	for _, tc := range []struct{ job, hp, mp, ap, sp int }{
		{1000, 12, 10, 6, 0}, {1110, 48, 4, 6, 3}, {2000, 12, 10, 5, 0}, {2110, 50, 4, 5, 3},
		{2001, 12, 10, 5, 0}, {2211, 12, 50, 5, 3}, {2310, 20, 14, 5, 3}, {2411, 20, 14, 5, 3},
		{3000, 12, 10, 5, 0}, {3110, 48, 0, 5, 3}, {3210, 20, 42, 5, 3}, {3510, 20, 18, 5, 3}, {431, 20, 14, 5, 3}, {531, 20, 18, 5, 3},
	} {
		c := domain.Character{Job: tc.job, Level: 30, MaxHP: 500, MaxMP: 200}
		if err := applyQuestExperience(&c, content.ExperienceForLevel(30), "family-growth"); err != nil {
			t.Fatalf("job%d: %v", tc.job, err)
		}
		if c.Level != 31 || c.MaxHP < 500+tc.hp || c.MaxHP > 504+tc.hp || c.MaxMP < 200+tc.mp || c.MaxMP > 205+tc.mp || c.AP != tc.ap || c.SP != tc.sp {
			t.Fatalf("job%d growth %+v", tc.job, c)
		}
	}
}
