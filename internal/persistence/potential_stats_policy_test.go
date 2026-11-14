package persistence

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestPotentialLegacyStoreBoundAndFixedSnailsPolicyAgree(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"Item.wz", "Character.wz/Weapon", "Skill.wz", "Mob.wz"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"Item.wz/ItemOption.img.xml":           `<imgdir name="ItemOption.img"><imgdir name="010001"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="incSTR" value="10"/></imgdir></imgdir></imgdir><imgdir name="020001"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="HP" value="5"/><int name="prop" value="20"/></imgdir></imgdir></imgdir></imgdir>`,
		"Character.wz/Weapon/01302000.img.xml": `<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`,
		"Skill.wz/100.img.xml":                 `<imgdir name="100.img"><imgdir name="skill"><imgdir name="1001004"><imgdir name="common"><int name="maxLevel" value="20"/><string name="mpCon" value="3+2*u(x/5)"/><string name="damage" value="180+5*x"/></imgdir></imgdir><imgdir name="1001005"><imgdir name="common"><int name="maxLevel" value="20"/><string name="mpCon" value="6+2*d(x/5)"/><string name="damage" value="120+5*x"/><int name="range" value="180"/><int name="mobCount" value="6"/></imgdir></imgdir></imgdir></imgdir>`,
		"Skill.wz/000.img.xml":                 `<imgdir name="000.img"><imgdir name="skill"><imgdir name="1000"><imgdir name="common"><int name="maxLevel" value="3"/><int name="fixdamage" value="10"/><int name="mpCon" value="3"/></imgdir></imgdir></imgdir></imgdir>`,
		// Synthetic high-HP target exposes a bound increase instead of hiding
		// it behind overkill. It is not an original-mob or MCP acceptance.
		"Mob.wz/9999998.img.xml": `<imgdir name="9999998.img"><imgdir name="info"><int name="maxHP" value="1000"/><int name="level" value="1"/><int name="exp" value="3"/></imgdir></imgdir>`,
	}
	for path, data := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	now := time.Now()
	c := domain.Character{ID: 1, Job: 100, Level: 30, HP: 100, MP: 100, MaxHP: 100, MaxMP: 100, STR: 100, DEX: 4, INT: 4, LUK: 4,
		Equipment: []domain.Equipment{{ID: 1, ItemID: 1302000, Slot: -11, WATK: 20, Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10001}}}},
		Skills:    []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}, {ID: 1000, Level: 1}}}
	q := domain.CombatAttackRequest{LinesPerTarget: 1, Session: domain.CharacterSession{CharacterID: 1, Generation: 1, SessionID: "potential-policy", Character: c}}
	for _, tc := range []struct {
		skill, damage, cost int
		family              domain.CombatFamily
	}{{0, 100, 0, domain.CombatNormal}, {1001004, 190, 5, domain.CombatPhysicalSkill}, {1001005, 130, 6, domain.CombatPhysicalSkill}, {1000, 10, 3, domain.CombatThreeSnails}} {
		t.Run(fmt.Sprint(tc.skill), func(t *testing.T) {
			q.Family, q.SkillID = tc.family, tc.skill
			cost, err := combatAttackCost(c, q, now)
			if err != nil || cost != tc.cost {
				t.Fatal("changed native WZ cost", cost, err)
			}
			target := domain.CombatTarget{MobID: 9999998, BeforeHP: 1000, Damage: tc.damage, Lines: []domain.CombatDamageLine{{Raw: uint32(tc.damage), Applied: tc.damage}}, Contributions: []domain.CombatContribution{{CharacterID: 1, Generation: 1, SessionID: q.Session.SessionID, Damage: int64(tc.damage)}}}
			rules := content.CombatExperienceRules{MaximumHP: 1000}
			if err := validateCombatTarget(c, q, target, rules, now); err != nil {
				t.Fatal("Store discarded effective bound", err)
			}
			if tc.skill == 1000 {
				target.Damage = 11
				target.Lines[0].Raw = 11
				target.Lines[0].Applied = 11
				target.Contributions[0].Damage = 11
				if err := validateCombatTarget(c, q, target, rules, now); err == nil {
					t.Fatal("fixed Snails damage amplified by potential")
				}
			} else {
				absent := c
				absent.Equipment = append([]domain.Equipment(nil), c.Equipment...)
				absent.Equipment[0].Potential = domain.EquipmentPotential{}
				if err := validateCombatTarget(absent, q, target, rules, now); err == nil {
					t.Fatal("fixture failed to distinguish actual potential benefit")
				}
			}
		})
	}
	c.Equipment[0].Potential.Options = [5]uint16{20001}
	for _, family := range []domain.CombatFamily{domain.CombatNormal, domain.CombatPhysicalSkill, domain.CombatThreeSnails} {
		q.Family = family
		if _, err := combatAttackCost(c, q, now); err == nil {
			t.Fatal("pending effect bypassed Store gate", family)
		}
	}
}
