package world

import (
	"context"
	"errors"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestDamageEvidenceFollowsActualHPWriteAndUnknownCommitResolutionOnce(t *testing.T) {
	t.Setenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS", "1,2")
	s, lives, sinks, mob := sharedWorldFixture(t)
	var pending *combatTicket
	var result domain.CombatAttackResult
	ready := false
	repo := &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}}
	repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
		result = fakeSharedResult(q)
		pending = mob.reservation
		return result, errors.New("receipt not yet observable")
	}
	repo.outcome = func(_, _ string) (*domain.CombatAttackResult, error) {
		if ready {
			return &result, nil
		}
		return nil, errors.New("lookup not available")
	}
	s.repo = repo
	before := mob.hp
	if err := s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 1, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal); err != nil {
		t.Fatal(err)
	}
	if pending == nil || len(pending.damageEvidence) != 0 || mob.hp != before {
		t.Fatal("uncertain commit must not fabricate an HP mutation")
	}
	ready = true
	s.resolveCombatSettlements(context.Background(), repo)
	if mob.hp != 0 || len(pending.damageEvidence) != 0 {
		t.Fatal("actual resolved damage was not applied and published once")
	}
	// Publication drains the captured evidence. Re-entering the same ticket
	// cannot claim another HP write after its reservation has been released.
	s.commitCombatTicket(pending, result)
	if len(pending.damageEvidence) != 0 {
		t.Fatal("repeated receipt fabricated another HP mutation")
	}
}
func TestDamageEvidenceRecordsCappedTargetHPAndDoesNotExposeSessionOrOperation(t *testing.T) {
	t.Setenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS", "7")
	q := domain.CombatTarget{ObjectID: 55, MobID: 100100, SpawnOperationID: "private-spawn", BeforeHP: 15, Damage: 15, Defeated: true,
		Lines: []domain.CombatDamageLine{{Raw: 100, Applied: 15}, {Raw: 100, Applied: 0}}}
	ticket := &combatTicket{request: domain.CombatAttackRequest{Session: domain.CharacterSession{CharacterID: 7, SessionID: "private-session"}, OperationID: "private-operation"}}
	ticket.recordAppliedDamageLocked(q, 0, true)
	if len(ticket.damageEvidence) != 1 {
		t.Fatal("missing actual target evidence")
	}
	e := ticket.damageEvidence[0]
	if e.BeforeHP != 15 || e.AfterHP != 0 || e.Applied != 15 || !e.Defeated || len(e.ClaimedLines) != 2 || len(e.SpawnHash) != 64 || e.SpawnHash == q.SpawnOperationID {
		t.Fatalf("incorrect bounded evidence: %+v", e)
	}
	if e.BoundedLines[0] != 15 || e.BoundedLines[1] != 0 {
		t.Fatal("evidence must retain actual post-cap values without inventing per-line HP writes")
	}
	t.Setenv("MAPLEGO_COMBAT_EVIDENCE_CHARACTERS", "70,0,bad")
	ticket.recordAppliedDamageLocked(q, 0, true)
	if len(ticket.damageEvidence) != 1 {
		t.Fatal("disabled trace captured an unrelated character")
	}
}
