package persistence

import (
	"encoding/hex"
	"errors"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
)

func TestCombatCommandAcceptsOriginalV148ThreeSnailsHit(t *testing.T) {
	wire, err := hex.DecodeString("0211e803000003ab14b526002c003afe99de0106de5f8e020000000009430f00070100706f9800015fffd7005fffd700f201280000005d2b923ef1d6856317ffd70000")
	if err != nil {
		t.Fatal(err)
	}
	attack, err := protocol.ReadBeginnerMagicAttack(protocol.NewReader(wire))
	if err != nil || len(attack.Targets) != 1 {
		t.Fatalf("decode native attack: %+v %v", attack, err)
	}
	request := domain.CombatAttackRequest{
		OperationID: "native-v148-three-snails-hit", ClientTick: attack.Tick,
		Family: domain.CombatThreeSnails, SkillID: 1000, LinesPerTarget: 1, Wire: wire,
		Members: []domain.CharacterSession{{}},
		Targets: []domain.CombatTarget{{ObjectID: attack.Targets[0].ObjectID, MobID: 100000,
			SpawnOperationID: "native-v148-three-snails-target", BeforeHP: 100, Damage: 40,
			Lines: []domain.CombatDamageLine{{Raw: 40, Applied: 40}}}},
	}
	if err := validateCombatCommand(request); err != nil {
		t.Fatalf("original 67-byte hit rejected: %v", err)
	}
	request.Wire = wire[:len(wire)-4]
	if err := validateCombatCommand(request); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("truncated packet validation: %v", err)
	}
}
