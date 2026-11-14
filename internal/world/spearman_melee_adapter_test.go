package world

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestSpearmanMeleeAdapterReachesReviewedSettlement(t *testing.T) {
	for _, skill := range []int{0, 1001004, 1001005} {
		t.Run(fmt.Sprint(skill), func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			c := &lives[0].session.Character
			c.Job, c.Level = 130, 30
			c.Skills = []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}}
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				if q.Session.Character.Job != 130 || q.SkillID != skill || len(q.Targets) != 1 || q.Targets[0].Damage <= 0 {
					t.Fatalf("wrong reviewed settlement: %+v", q)
				}
				return fakeSharedResult(q), nil
			}}
			attack := protocol.MeleeAttack{Skill: skill, Tick: 81, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}
			var err error
			cost := 0
			if skill == 0 {
				err = s.meleeGameplay(context.Background(), 1, lives[0], sinks[0], attack)
			} else {
				cost = 6
				err = s.physicalSkillGameplay(context.Background(), 1, lives[0], sinks[0], attack)
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || mob.hp != 0 || c.MP != 100-cost || c.EXP != 3 || c.Revision != 2 {
				t.Fatalf("Spearman front door did not settle once: calls=%d mobHP=%d character=%+v", calls, mob.hp, c)
			}
			if sinks[1].count(protocol.SendKillMonster) != 1 {
				t.Fatal("peer did not see one settled death")
			}
		})
	}
}

func TestSpearmanMeleeAdapterDoesNotEnableOtherJobsOrInvalidState(t *testing.T) {
	for _, job := range []int{110, 120, 131, 200, 300, 400, 500} {
		t.Run(fmt.Sprint(job), func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			lives[0].session.Character.Job, lives[0].session.Character.Level = job, 30
			calls := 0
			s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				return fakeSharedResult(q), nil
			}}
			before := mob.hp
			if err := s.meleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 82, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}); err != nil {
				t.Fatal(err)
			}
			if calls != 0 || mob.hp != before || lives[0].session.Character.MP != 100 {
				t.Fatal("unsupported job gained a melee route")
			}
		})
	}
	for _, dead := range []bool{false, true} {
		s, lives, sinks, mob := sharedWorldFixture(t)
		lives[0].session.Character.Job, lives[0].session.Character.Level = 130, 29
		if dead {
			lives[0].session.Character.Level, lives[0].session.Character.HP = 30, 0
		}
		calls := 0
		s.repo = &sharedCombatRepo{apply: func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
			calls++
			return fakeSharedResult(q), nil
		}}
		before := mob.hp
		if err := s.meleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 83, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}); err != nil {
			t.Fatal(err)
		}
		if calls != 0 || mob.hp != before {
			t.Fatal("invalid Spearman state entered settlement")
		}
	}
}
