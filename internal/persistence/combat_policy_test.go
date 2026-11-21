package persistence

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
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

func TestMechanicSiegeAndMissileTankSettlementLines(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	now := time.Unix(30, 0)
	for _, tc := range []struct {
		id, level int
	}{
		{35111004, 20}, // Heavy Machine Gun (Siege Mode)
		{35121005, 30}, // Missile Tank
		{35121013, 20}, // Siege Mode 2 (linked from 35111004)
	} {
		ch := domain.Character{Job: 3512, HP: 5000, MP: 5000,
			Skills:     []domain.SkillState{{ID: 35111004, Level: 20}, {ID: 35121005, Level: 30}},
			SkillBuffs: []domain.SkillBuff{{ID: tc.id, Level: tc.level, Stat: "MECH_CHANGE", Value: tc.level, ExpiresAt: now.Add(time.Hour)}}}
		res, ongoing, err := skills.AttackResourceCost(ch, tc.id, tc.level, protocol.RecvShootAttack, now)
		if err != nil {
			t.Fatalf("mech mode %d AttackResourceCost: %v", tc.id, err)
		}
		q := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: tc.id,
			Targets: make([]domain.CombatTarget, 6), LinesPerTarget: 6,
			Cost: &domain.CombatCost{Level: tc.level, MP: res.MP, CooldownMS: int(res.Cooldown.Milliseconds()), Ongoing: ongoing, Opcode: protocol.RecvShootAttack}}
		if mp, err := genericCombatCost(ch, q, now); err != nil || mp != res.MP {
			t.Fatalf("mech mode %d rejected native 6x6 attack: mp %d, %v", tc.id, mp, err)
		}
		q.Targets = append(q.Targets, domain.CombatTarget{})
		if _, err := genericCombatCost(ch, q, now); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("mech mode %d accepted 7 targets: %v", tc.id, err)
		}
	}
}

func TestTempestOfCardKeydownSpinOngoingWindow(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	start := time.Unix(100, 0)
	ch := domain.Character{Job: 2412, HP: 5000, MP: 5000,
		Skills: []domain.SkillState{{ID: 24121005, Level: 30}}}
	// Frame 1: starts 12s cooldown (15-d(30/10)=12) and pays MP=30 (25+d(30/6)=30).
	res, ongoing, err := skills.AttackResourceCost(ch, 24121005, 30, protocol.RecvMeleeAttack, start)
	if err != nil || ongoing || res.MP != 30 || res.Cooldown != 12*time.Second {
		t.Fatalf("frame 1 AttackResourceCost: res=%+v ongoing=%v err=%v", res, ongoing, err)
	}
	q1 := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: 24121005,
		Targets: make([]domain.CombatTarget, 1), LinesPerTarget: 1,
		Cost: &domain.CombatCost{Level: 30, MP: res.MP, CooldownMS: int(res.Cooldown.Milliseconds()), Ongoing: false, Opcode: protocol.RecvMeleeAttack}}
	if mp, err := genericCombatCost(ch, q1, start); err != nil || mp != 30 {
		t.Fatalf("frame 1 genericCombatCost: mp=%d err=%v", mp, err)
	}
	// Active 12s cooldown set by frame 1.
	ch.SkillCooldowns = []domain.SkillCooldown{{ID: 24121005, ExpiresAt: start.Add(12 * time.Second)}}
	// Frame 2 (+180ms) and mid-spin (+4s): inside 5s keydown window -> ongoing=true, CooldownMS=0.
	for _, delta := range []time.Duration{180 * time.Millisecond, 4 * time.Second} {
		at := start.Add(delta)
		res, ongoing, err := skills.AttackResourceCost(ch, 24121005, 30, protocol.RecvMeleeAttack, at)
		if err != nil || !ongoing || res.MP != 30 || res.Cooldown != 0 {
			t.Fatalf("+%s AttackResourceCost: res=%+v ongoing=%v err=%v", delta, res, ongoing, err)
		}
		q := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: 24121005,
			Targets: make([]domain.CombatTarget, 1), LinesPerTarget: 1,
			Cost: &domain.CombatCost{Level: 30, MP: res.MP, CooldownMS: 0, Ongoing: true, Opcode: protocol.RecvMeleeAttack}}
		if mp, err := genericCombatCost(ch, q, at); err != nil || mp != 30 {
			t.Fatalf("+%s genericCombatCost: mp=%d err=%v", delta, mp, err)
		}
	}
	// After 5s window (+6s): remaining cooldown (6s) blocks new cast.
	at := start.Add(6 * time.Second)
	res, ongoing, err = skills.AttackResourceCost(ch, 24121005, 30, protocol.RecvMeleeAttack, at)
	if err != nil || ongoing || res.Cooldown != 12*time.Second {
		t.Fatalf("+6s AttackResourceCost: res=%+v ongoing=%v err=%v", res, ongoing, err)
	}
	qLate := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: 24121005,
		Targets: make([]domain.CombatTarget, 1), LinesPerTarget: 1,
		Cost: &domain.CombatCost{Level: 30, MP: res.MP, CooldownMS: int(res.Cooldown.Milliseconds()), Ongoing: false, Opcode: protocol.RecvMeleeAttack}}
	if _, err := genericCombatCost(ch, qLate, at); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("+6s should be blocked by remaining cooldown: err=%v", err)
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
