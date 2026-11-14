package persistence

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestReviewedCriticalityStoreRetainsDurableBoundsAndLegacyReceipts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Mob.wz"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Mob.wz", "9999998.img.xml"), []byte(`<imgdir name="9999998.img"><imgdir name="info"><int name="maxHP" value="550000000"/><int name="level" value="129"/><int name="PDRate" value="50"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	q := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: 1311001, LinesPerTarget: 3, Cost: &domain.CombatCost{Opcode: protocol.RecvMeleeAttack}, Session: domain.CharacterSession{CharacterID: 1, Generation: 1, SessionID: "critical-policy"}}
	target := domain.CombatTarget{MobID: 9999998, BeforeHP: 550000000, Damage: 1048, Lines: []domain.CombatDamageLine{{Raw: 426, Applied: 426, Criticality: domain.CombatCriticalUnknown}, {Raw: 276, Applied: 276, Criticality: domain.CombatCriticalUnknown}, {Raw: 346, Applied: 346, Criticality: domain.CombatCriticalUnknown}}, Contributions: []domain.CombatContribution{{CharacterID: 1, Generation: 1, SessionID: q.Session.SessionID, Damage: 1048}}}
	rules := content.CombatExperienceRules{MaximumHP: 550000000}
	if err := validateCombatTarget(domain.Character{}, q, target, rules, time.Now()); err != nil {
		t.Fatal("world legal unknown lines rejected", err)
	}
	legacy := target
	legacy.Lines = append([]domain.CombatDamageLine(nil), target.Lines...)
	for i := range legacy.Lines {
		legacy.Lines[i].Criticality = ""
	}
	if err := validateCombatTarget(domain.Character{}, q, legacy, rules, time.Now()); err != nil {
		t.Fatal("old empty-metadata receipt rejected", err)
	}
	for _, which := range []string{"false-explicit", "wrong-skill", "above-raw", "above-display", "wrong-opcode"} {
		t.Run(which, func(t *testing.T) {
			qq := q
			bad := target
			bad.Lines = append([]domain.CombatDamageLine(nil), target.Lines...)
			switch which {
			case "false-explicit":
				bad.Lines[0].Criticality = domain.CombatCriticalExplicit
			case "wrong-skill":
				qq.SkillID = 1311003
			case "above-raw":
				bad.Lines[0].Applied = 427
			case "above-display":
				bad.Lines[0].Raw = 1000001
				bad.Lines[0].Applied = 1000000
			case "wrong-opcode":
				cost := *qq.Cost
				cost.Opcode = protocol.RecvShootAttack
				qq.Cost = &cost
			}
			if err := validateCombatTarget(domain.Character{}, qq, bad, rules, time.Now()); err == nil {
				t.Fatal("forged line metadata/bound accepted")
			}
		})
	}
}
