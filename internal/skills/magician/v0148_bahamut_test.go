package magician

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestBahamutSummonPassiveASR(t *testing.T) {
	ch := domain.Character{Job: 232, Skills: []domain.SkillState{{ID: 2321003, Level: 30}}}
	b := skills.Bonuses(ch, time.Unix(100, 0))
	if b.ASR != 40 {
		t.Fatalf("Bahamut (2321003) lv30 ASR = %d, want 40", b.ASR)
	}
}
