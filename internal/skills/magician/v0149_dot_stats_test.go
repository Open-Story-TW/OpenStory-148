package magician

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"testing"
	"time"
)

func TestElementalDrainAndHolyFocusPlayerStats(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	base := domain.Character{Job: 212, Level: 200, STR: 100, DEX: 100, INT: 500, LUK: 100}
	fp := base
	fp.Skills = []domain.SkillState{{ID: 2110000, Level: 10}}
	pFP := skills.PlayerStats(fp, now)
	if pFP.DOTTime != 10 || pFP.DOTPercent != 10 {
		t.Fatalf("2110000 Elemental Drain FP PlayerStats = DOTTime:%d DOTPercent:%d, want 10/10", pFP.DOTTime, pFP.DOTPercent)
	}

	il := domain.Character{Job: 222, Level: 200, STR: 100, DEX: 100, INT: 500, LUK: 100,
		Skills: []domain.SkillState{{ID: 2210000, Level: 10}}}
	pIL := skills.PlayerStats(il, now)
	if pIL.DOTTime != 0 || pIL.DOTPercent != 10 {
		t.Fatalf("2210000 Elemental Drain IL PlayerStats = DOTTime:%d DOTPercent:%d, want 0/10", pIL.DOTTime, pIL.DOTPercent)
	}

	bishop0 := domain.Character{Job: 232, Level: 200, STR: 100, DEX: 100, INT: 500, LUK: 100}
	bishop1 := bishop0
	bishop1.Skills = []domain.SkillState{{ID: 2310008, Level: 20}}
	s0 := skills.PlayerStats(bishop0, now)
	s1 := skills.PlayerStats(bishop1, now)
	if s1.Accuracy <= s0.Accuracy || s1.MagicAccuracy <= s0.MagicAccuracy {
		t.Fatalf("2310008 Holy Focus ACCPercent=15 did not scale accuracy: before=%d/%d after=%d/%d",
			s0.Accuracy, s0.MagicAccuracy, s1.Accuracy, s1.MagicAccuracy)
	}
}
