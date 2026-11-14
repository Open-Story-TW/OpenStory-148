package persistence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func writeResourceCommandWZ(t *testing.T) string {
	t.Helper()
	root := writePotentialCommandTestWZ(t)
	var option strings.Builder
	option.WriteString(`<imgdir name="ItemOption.img"><imgdir name="040502"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level">`)
	for band := 1; band <= 20; band++ {
		fmt.Fprintf(&option, `<imgdir name="%d"><int name="mpconReduce" value="33"/><int name="reduceCooltime" value="7"/></imgdir>`, band)
	}
	option.WriteString(`</imgdir></imgdir></imgdir>`)
	files := map[string]string{
		"Item.wz/ItemOption.img.xml": option.String(),
		"Skill.wz/211.img.xml":       `<imgdir name="211.img"><imgdir name="skill"><imgdir name="2110001"><imgdir name="common"><int name="maxLevel" value="30"/><int name="x" value="150"/><int name="y" value="150"/></imgdir></imgdir></imgdir></imgdir>`,
		"Skill.wz/212.img.xml":       `<imgdir name="212.img"><imgdir name="skill"><imgdir name="2121003"><imgdir name="common"><int name="maxLevel" value="30"/><int name="mpCon" value="7"/><int name="cooltime" value="10"/><int name="damage" value="100"/></imgdir></imgdir></imgdir></imgdir>`,
		"Skill.wz/100.img.xml":       `<imgdir name="100.img"><imgdir name="skill"><imgdir name="1001003"><imgdir name="common"><int name="maxLevel" value="20"/><int name="mpCon" value="7"/><int name="cooltime" value="10"/><int name="time" value="60"/><int name="pdd" value="20"/></imgdir></imgdir><imgdir name="1001004"><imgdir name="common"><int name="maxLevel" value="20"/><int name="mpCon" value="5"/><int name="damage" value="190"/></imgdir></imgdir><imgdir name="1001005"><imgdir name="common"><int name="maxLevel" value="20"/><int name="mpCon" value="6"/><int name="damage" value="130"/><int name="mobCount" value="6"/><int name="range" value="180"/></imgdir></imgdir></imgdir></imgdir>`,
		"Skill.wz/000.img.xml":       `<imgdir name="000.img"><imgdir name="skill"><imgdir name="1000"><imgdir name="common"><int name="maxLevel" value="3"/><int name="mpCon" value="3"/><int name="fixdamage" value="10"/></imgdir></imgdir></imgdir></imgdir>`,
	}
	files["Skill.wz/130.img.xml"] = `<imgdir name="130.img"><imgdir name="skill"><imgdir name="1301007"><imgdir name="common"><int name="maxLevel" value="20"/><int name="mpCon" value="7"/><int name="cooltime" value="10"/><int name="time" value="60"/><int name="mhpR" value="60"/><int name="mmpR" value="60"/><vector name="lt" x="-300" y="-100"/><vector name="rb" x="300" y="100"/></imgdir></imgdir></imgdir></imgdir>`
	for path, data := range files {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func resourceCommandCharacter() domain.Character {
	return domain.Character{ID: 1, Job: 212, Level: 150, HP: 100, MP: 100, MaxHP: 100, MaxMP: 100, STR: 100, DEX: 4, INT: 100, LUK: 4,
		Skills:    []domain.SkillState{{ID: 2110001, Level: 1}, {ID: 2121003, Level: 1}},
		Equipment: []domain.Equipment{{ID: 1, Slot: -11, ItemID: 1322005, WATK: 20, Potential: domain.EquipmentPotential{Present: true, RawState: 20, Options: [5]uint16{40502}}}}}
}

func TestResourceStoreRecomputesExactGenericCostAndCooldown(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", writeResourceCommandWZ(t))
	c := resourceCommandCharacter()
	now := time.Now()
	q := domain.CombatAttackRequest{Family: domain.CombatSkill, SkillID: 2121003, LinesPerTarget: 1, Cost: &domain.CombatCost{Opcode: protocol.RecvMagicAttack, MP: 7, Level: 1, CooldownMS: 3000}}
	if n, err := genericCombatCost(c, q, now); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	for _, mutate := range []func(*domain.CombatCost){
		func(p *domain.CombatCost) { p.MP = 0 }, func(p *domain.CombatCost) { p.MP = 6 }, func(p *domain.CombatCost) { p.MP = 8 },
		func(p *domain.CombatCost) { p.CooldownMS = 0 }, func(p *domain.CombatCost) { p.CooldownMS = 2999 }, func(p *domain.CombatCost) { p.CooldownMS = 3001 },
		func(p *domain.CombatCost) { p.Ongoing = true }, func(p *domain.CombatCost) { p.Opcode = protocol.RecvBodyAttack }, func(p *domain.CombatCost) { p.Summon = 123 },
	} {
		cost := *q.Cost
		mutate(&cost)
		bad := q
		bad.Cost = &cost
		if _, err := genericCombatCost(c, bad, now); err == nil {
			t.Fatal("altered cost accepted", cost)
		}
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 2121004, Stat: "INFINITY", Value: 1, ExpiresAt: now.Add(time.Minute)}}
	q.Cost.MP = 0
	if n, err := genericCombatCost(c, q, now); err != nil || n != 0 {
		t.Fatal("Infinity not shared", n, err)
	}
	c.SkillBuffs[0].Value = 0
	if _, err := genericCombatCost(c, q, now); err == nil {
		t.Fatal("zero Infinity marker allowed free cost")
	}
	c.SkillBuffs[0].Value = 1
	c.SkillBuffs[0].ExpiresAt = now
	if _, err := genericCombatCost(c, q, now); err == nil {
		t.Fatal("expired Infinity allowed free cost")
	}
	q.Cost.MP = 7
	c.SkillCooldowns = []domain.SkillCooldown{{ID: q.SkillID, ExpiresAt: now.Add(time.Second)}}
	if _, err := genericCombatCost(c, q, now); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatal("active cooldown bypassed", err)
	}
}

func TestResourceLegacyCostsAndStoreDoesNotRerollPrepare(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", writeResourceCommandWZ(t))
	c := resourceCommandCharacter()
	c.Job = 100
	c.Skills = []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}, {ID: 1000, Level: 1}}
	now := time.Now()
	for _, tc := range []struct {
		id, mp int
		family domain.CombatFamily
	}{{0, 0, domain.CombatNormal}, {1001004, 4, domain.CombatPhysicalSkill}, {1001005, 5, domain.CombatPhysicalSkill}, {1000, 3, domain.CombatThreeSnails}} {
		q := domain.CombatAttackRequest{Family: tc.family, SkillID: tc.id}
		if n, err := combatAttackCost(c, q, now); err != nil || n != tc.mp {
			t.Fatal(tc, n, err)
		}
	}
	// General Prepare may contain a projectile RNG roll. Persistence must not
	// run it, including for skills whose cost source is otherwise unchanged.
	c.Job = 212
	c.Skills = []domain.SkillState{{ID: 2110001, Level: 1}, {ID: 2121003, Level: 1}}
	ctx := combat.AttackContext{Kind: combat.Magic, Character: c, Now: now, Effect: combat.Effect{SkillID: 2121003, MPCon: 7, Cooltime: 10 * time.Second}}
	ctx.Stats.ResourceCosts.Prepared = true
	ctx.Stats.ResourceCosts.MPConsumePercent = 150
	ctx.Stats.ResourceCosts.MPReductionPercent = 33
	ctx.Stats.ResourceCosts.CooldownReductionSeconds = 7
	rngCalls := 0
	spec := &combat.AttackSpec{Kind: combat.Magic, Prepare: func(*combat.AttackContext) error { rngCalls++; return nil }}
	if out, err := combat.PrepareResourceCost(spec, ctx); err != nil || out.MP != 7 || out.Cooldown != 3*time.Second || rngCalls != 0 {
		t.Fatal("Store rerolled Prepare", out, rngCalls, err)
	}
}
