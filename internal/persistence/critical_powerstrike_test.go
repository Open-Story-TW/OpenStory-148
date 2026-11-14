package persistence

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestReviewedPowerStrikeStoreCriticalityCostAndLegacyReceipt(t *testing.T) {
	now := time.Now()
	c := domain.Character{ID: 1, Job: 132, Level: 150, HP: 3220, MP: 1454, MaxHP: 3220, MaxMP: 1454, STR: 57, DEX: 500, INT: 4, LUK: 4,
		Equipment: []domain.Equipment{{Slot: -11, ItemID: 1432000, WATK: 32}}, Skills: []domain.SkillState{{ID: 1001004, Level: 20}}}
	q := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: 1001004, LinesPerTarget: 1,
		Cost: &domain.CombatCost{Opcode: protocol.RecvMeleeAttack, Level: 20, MP: 11}, Session: domain.CharacterSession{CharacterID: 1, Generation: 1, SessionID: "powerstrike-critical-policy"}}
	target := domain.CombatTarget{MobID: 9300306, BeforeHP: 550000000, Damage: 653,
		Lines:         []domain.CombatDamageLine{{Raw: 653, Applied: 653, Criticality: domain.CombatCriticalUnknown}},
		Contributions: []domain.CombatContribution{{CharacterID: 1, Generation: 1, SessionID: q.Session.SessionID, Damage: 653}}}
	q.Targets = []domain.CombatTarget{target}
	rules := content.CombatExperienceRules{MaximumHP: 550000000}
	if err := validateCombatTarget(c, q, target, rules, now); err != nil {
		t.Fatal("legal unknown target rejected", err)
	}
	if mp, err := combatAttackCost(c, q, now); err != nil || mp != 11 {
		t.Fatal("fresh original WZ cost", mp, err)
	}
	legacy := target
	legacy.Lines = append([]domain.CombatDamageLine(nil), target.Lines...)
	legacy.Lines[0].Criticality = ""
	if err := validateCombatTarget(c, q, legacy, rules, now); err != nil {
		t.Fatal("legacy empty-metadata receipt rejected", err)
	}
	explicit := target
	explicit.Lines = append([]domain.CombatDamageLine(nil), target.Lines...)
	explicit.Lines[0].Raw |= 0x80000000
	explicit.Lines[0].Criticality = domain.CombatCriticalExplicit
	if err := validateCombatTarget(c, q, explicit, rules, now); err != nil {
		t.Fatal("real explicit bit rejected", err)
	}
	for _, name := range []string{"falseExplicit", "wrongSkill1001005", "wrongOpcode", "missingCost", "aboveRaw", "aboveDisplay", "falseUnknownOnExplicit", "unpayableCost"} {
		t.Run(name, func(t *testing.T) {
			qq := q
			cost := *q.Cost
			qq.Cost = &cost
			bad := target
			bad.Lines = append([]domain.CombatDamageLine(nil), target.Lines...)
			switch name {
			case "falseExplicit":
				bad.Lines[0].Criticality = domain.CombatCriticalExplicit
			case "wrongSkill1001005":
				qq.SkillID = 1001005
			case "wrongOpcode":
				qq.Cost.Opcode = protocol.RecvShootAttack
			case "missingCost":
				qq.Cost = nil
			case "aboveRaw":
				bad.Lines[0].Applied = 654
			case "aboveDisplay":
				bad.Lines[0].Raw = 1000001
				bad.Lines[0].Applied = 1000000
			case "falseUnknownOnExplicit":
				bad.Lines[0].Raw |= 0x80000000
			case "unpayableCost":
				qq.Cost.MP = 10
			}
			if name == "unpayableCost" {
				if _, err := combatAttackCost(c, qq, now); err == nil {
					t.Fatal("undercharged cost accepted")
				}
				return
			}
			if err := validateCombatTarget(c, qq, bad, rules, now); err == nil {
				t.Fatal("forged metadata or bound accepted")
			}
		})
	}
}
