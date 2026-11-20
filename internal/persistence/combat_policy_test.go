package persistence

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestChargedBlowSettlementUsesLearnedWZTargetLimit(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	now := time.Unix(30, 0)
	ch := domain.Character{Job: 122, HP: 500, MP: 500,
		Skills: []domain.SkillState{{ID: 1211002, Level: 30}, {ID: 1220010, Level: 10}}}
	q := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: 1211002,
		Targets: make([]domain.CombatTarget, 6), LinesPerTarget: 2,
		Cost: &domain.CombatCost{Level: 30, MP: 30, Opcode: protocol.RecvMeleeAttack}}
	if mp, err := genericCombatCost(ch, q, now); err != nil || mp != 30 {
		t.Fatalf("learned Elemental Force rejected native 6x2 attack: mp %d, %v", mp, err)
	}
	q.Targets = append(q.Targets, domain.CombatTarget{})
	if _, err := genericCombatCost(ch, q, now); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("7 targets should exceed the learned upgrade: %v", err)
	}
	q.Targets = q.Targets[:6]
	ch.Skills = ch.Skills[:1]
	if _, err := genericCombatCost(ch, q, now); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("6 targets accepted before learning Elemental Force: %v", err)
	}
}

func TestCombatCommandDoesNotRejectAlreadyAdmittedHistoryByCount(t *testing.T) {
	q := domain.CombatAttackRequest{OperationID: "combat-historical-generations", Family: domain.CombatNormal,
		Targets: []domain.CombatTarget{{ObjectID: 1, MobID: 9300018, SpawnOperationID: "mob-historical-generations", BeforeHP: 1, Damage: 1, Defeated: true}}}
	for i := int64(1); i <= 600; i++ {
		q.Targets[0].Contributions = append(q.Targets[0].Contributions, domain.CombatContribution{CharacterID: i, Generation: i, SessionID: "history", Damage: 1})
		q.Members = append(q.Members, domain.CharacterSession{CharacterID: i})
	}
	// Per-member authentication and the actual WZ HP sum remain mandatory in
	// ApplyCombatAttack. This shape check must not invent a smaller count limit
	// than the room that already admitted positive effective damage.
	if err := validateCombatCommand(q); err != nil {
		t.Fatalf("count-only rejection stranded an admitted ledger: %v", err)
	}
}

func TestBonusExperience(t *testing.T) {
	// The cap under test is the stock 32-bit EXP limit.
	defer domain.SetLongExperienceForTest(false)()
	for _, c := range []struct {
		exp     int64
		percent int
		want    int64
	}{{100, 0, 100}, {100, 10, 110}, {7, 15, 8}, {0, 50, 0}, {2147483000, 50, 2147483647}} {
		if got := bonusExperience(c.exp, c.percent); got != c.want {
			t.Errorf("bonusExperience(%d,%d)=%d want %d", c.exp, c.percent, got, c.want)
		}
	}
}
