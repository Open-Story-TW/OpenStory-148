package world

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func TestLearnedThreeSnailsRemainsUsableAfterAdventurerAdvancement(t *testing.T) {
	for _, job := range []int{0, 100, 200, 300, 400, 500, 600, 1000} {
		t.Run(fmt.Sprint(job), func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			lives[0].session.Character.Job = job
			lives[0].session.Character.Skills = []domain.SkillState{{ID: 1000, Level: 1}}
			mob.life.X = 80
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return fakeSharedResult(q), nil
			}}
			if err := s.magicGameplay(context.Background(), 1, lives[0], sinks[0], worldMagic(t, mob.objectID, 10, 0, 0)); err != nil {
				t.Fatal(err)
			}
			want := 1
			if job >= 600 {
				want = 0
			}
			if calls != want {
				t.Fatalf("job %d: durable attack calls=%d want=%d", job, calls, want)
			}
		})
	}
}
