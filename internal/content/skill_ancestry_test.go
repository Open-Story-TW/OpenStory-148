package content

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestGMSkillBooksNeedTheGMFlag(t *testing.T) {
	gm := Skill{ID: 9001000}
	if gm.CanLearnBy(domain.Character{Job: 0}) {
		t.Fatal("an ordinary beginner must not learn GM skills")
	}
	if !gm.CanLearnBy(domain.Character{Job: 0, GM: 6}) {
		t.Fatal("a GM-flagged QA character learns GM skills")
	}
	if (Skill{ID: 1001003}).CanLearnBy(domain.Character{Job: 0, GM: 6}) {
		t.Fatal("the GM flag must not open other job books")
	}
}
