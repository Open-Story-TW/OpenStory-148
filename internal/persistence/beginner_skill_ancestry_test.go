package persistence

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestThreeSnailsCostUsesAdventurerAncestryAndLiveSkill(t *testing.T) {
	now := time.Now()
	for _, job := range []int{0, 100, 110, 200, 300, 400, 500, -1, 600, 1000} {
		c := domain.Character{Job: job, HP: 50, Skills: []domain.SkillState{{ID: 1000, Level: 1}}}
		cost, err := combatAttackCost(c, domain.CombatAttackRequest{Family: domain.CombatThreeSnails}, now)
		if job >= 0 && job < 600 {
			if err != nil || cost != 3 {
				t.Fatalf("job %d: cost=%d err=%v", job, cost, err)
			}
		} else if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("unrelated job %d allowed: %v", job, err)
		}
	}
	for _, state := range []domain.SkillState{{ID: 1000}, {ID: 1000, Level: 1, ExpiresAt: now.Add(-time.Second)}} {
		c := domain.Character{Job: 100, HP: 50, Skills: []domain.SkillState{state}}
		if _, err := combatAttackCost(c, domain.CombatAttackRequest{Family: domain.CombatThreeSnails}, now); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("missing/expired learned skill allowed after advancement: %v", err)
		}
	}
}

func TestPersistedThreeSnailsAfterFirstJobUsesSharedSettlementAndReplay(t *testing.T) {
	ctx, s, actors := settlementFixture(t)
	allocated, err := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: actors[0], SkillID: 1000, OperationID: "ancestry-learn-before-first-job"})
	if err != nil {
		t.Fatal(err)
	}
	actors[0].Character = allocated.Character
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET job=100 WHERE id=$1`, actors[0].CharacterID); err != nil {
		t.Fatal(err)
	}
	actors[0].Character.Job = 100
	q := settlementRequest(t, ctx, s, actors, domain.CombatThreeSnails, 1)
	beforeMP := actors[0].Character.MP
	result, err := s.ApplyCombatAttack(ctx, q)
	if err != nil {
		t.Fatalf("learned beginner skill rejected after first job: %v", err)
	}
	replay, err := s.ApplyCombatAttack(ctx, q)
	if err != nil || !replay.Replayed {
		t.Fatalf("same attack replay: %+v %v", replay, err)
	}
	actor, err := s.GetCharacter(ctx, actors[0].CharacterID)
	if err != nil || actor.Job != 100 || actor.MP != beforeMP-3 {
		t.Fatalf("first-job attack cost/identity: %s %+v %v", fmt.Sprint(result.OperationID), actor, err)
	}
}
