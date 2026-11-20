package pirate

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// Lane B long10 captured a critical regional Ice Splitter line of
// 13,878,606,193 that the world clipped to 13,815,898,620: the normal bound
// 7,675,499,233 (WZ damage 340) times 1.8 (Sharp Eyes). The v148 client adds
// the learned 5920001 damage (80 at level 30) to the skill's percent
// (0x712C51), so the bound must be computed from 420.
func TestRegionalElementalBoostRaisesTheIceSplitterBound(t *testing.T) {
	defer protocol.SetLongDamageCap(0)
	protocol.SetLongDamageCap(protocol.LongDamageMaxCap)
	now := time.Unix(100, 0)
	c := domain.Character{ID: 4918, Job: 591, Level: 200, HP: 30000, MaxHP: 30000, MP: 30000, MaxMP: 30000,
		STR: 9_999_999, DEX: 9_999_999, INT: 9_999_999, LUK: 9_999_999,
		Equipment:  []domain.Equipment{{Slot: -11, ItemID: 1492001, WATK: 3000}},
		Skills:     []domain.SkillState{{ID: 5900000, Level: 5}, {ID: 5911005, Level: 20}},
		SkillBuffs: []domain.SkillBuff{{ID: 3121002, Level: 30, Stat: "SHARP_EYES", Value: 20<<8 | 30, ExpiresAt: now.Add(time.Hour)}}}
	plan := func(c domain.Character) combat.TargetPlan {
		t.Helper()
		stats := skills.PlayerStats(c, now)
		stats.WATK += 10 // the bullet in the named slot (2330000)
		h, _ := skills.Lookup(5911005)
		def, _ := values(t, 5911005, 20)
		e, err := combat.FromSkill(def, 20)
		if err != nil {
			t.Fatal(err)
		}
		e.Box = combat.Box{}
		a := protocol.Attack{Opcode: protocol.RecvShootAttack, SkillID: 5911005, Slot: 6, TByte: 0x11,
			Targets: []protocol.AttackTarget{{ObjectID: 5, Lines: []protocol.AttackLine{{Raw: 0xFF000010, Long: 13_878_606_193}}}}}
		mob := content.Mob{Level: 10, HP: 100_000_000_000}
		c.Items = []domain.InventoryItem{{InvType: 2, Slot: 6, TemplateID: 2330000, Quantity: 700}}
		p, err := combat.PlanAttack(h.Attack, combat.AttackContext{Kind: combat.Shoot, Character: c, Stats: stats, Effect: e, Attack: a, Now: now,
			Targets: []combat.TargetInfo{{ObjectID: 5, Mob: mob, HP: mob.HP}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p.Targets[0]
	}
	without := plan(c)
	if without.Normal[0] != 7_675_499_233 || without.Lines[0] != 13_815_898_620 {
		t.Fatalf("capture not reproduced: normal %d, line %d", without.Normal[0], without.Lines[0])
	}
	c.Skills = append(c.Skills, domain.SkillState{ID: 5920001, Level: 30})
	with := plan(c)
	if with.Lines[0] != 13_878_606_193 || with.Clipped != 0 || with.Normal[0] < 7_675_499_233*420/340 {
		t.Fatalf("elemental boost not in the bound: %+v", with)
	}
}
