package persistence

import (
	"testing"

	"maple-go-server/internal/domain"
)

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
