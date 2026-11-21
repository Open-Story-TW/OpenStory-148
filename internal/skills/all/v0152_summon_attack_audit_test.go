package all_test

import (
	"os"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

func TestSummonEffectSelfDestructionAndDarkFlareCeiling(t *testing.T) {
	// Bots 'n Tots child robot (35121011) uses WZ selfDestruction (200+5*x = 350 at lv 30)
	// and summon/attack1 mobCount=4 with no common "damage" key.
	botDef := content.Skill{
		ID:           35121011,
		MaxLevel:     30,
		Common:       map[string]string{"maxLevel": "30", "time": "5", "selfDestruction": "200+5*x"},
		SummonAttack: map[string]int{"mobCount": 4},
	}
	eff, err := combat.SummonEffect(botDef, 30)
	if err != nil {
		t.Fatalf("SummonEffect(35121011, 30): %v", err)
	}
	if eff.Damage != 350 || eff.MobCount != 4 || eff.AttackCount != 1 {
		t.Fatalf("35121011 SummonEffect = %+v, want Damage=350 MobCount=4 AttackCount=1", eff)
	}

	// Dark Flare (4111007 / 4211007) reflects x% of monster contact damage up to mob.HP/2.
	for _, id := range []int{4111007, 4211007} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || h.Attack.MaxDamage == nil {
			t.Fatalf("skill %d: missing Attack or MaxDamage", id)
		}
		mob := content.Mob{ID: 8190003, Level: 120, HP: 500_000}
		dc := combat.DamageContext{
			Stats:  combat.PlayerStats{Job: id / 10000, Level: 200, LUK: 100, WATK: 30},
			Effect: combat.Effect{SkillID: id, Damage: 100, MobCount: 1, AttackCount: 1},
			Mob:    mob,
		}
		got := h.Attack.MaxDamage(dc)
		if got != 250_000 {
			t.Fatalf("skill %d MaxDamage(HP=500000) = %d, want 250000", id, got)
		}
		inv := dc
		inv.Mob.Invincible = true
		if h.Attack.MaxDamage(inv) != 0 {
			t.Fatalf("skill %d MaxDamage on invincible mob must be 0", id)
		}
		fix := dc
		fix.Mob.HasFixedDamage = true
		fix.Mob.FixedDamage = 10
		if h.Attack.MaxDamage(fix) != 10 {
			t.Fatalf("skill %d MaxDamage on fixed-damage mob = %d, want 10", id, h.Attack.MaxDamage(fix))
		}
	}
}

func TestAudit30SummonAttacks(t *testing.T) {
	wzRoot := os.Getenv("MAPLEGO_WZ_ROOT")
	if wzRoot == "" {
		t.Skip("MAPLEGO_WZ_ROOT not set")
	}
	ids := []int{
		2121005, 2221005, 2321003,
		3101007, 3111005, 3120012, 3201007, 3211005, 3220012,
		4111007, 4211007,
		5211011, 5211014, 5321003, 5321004, 5711001, 5911001, 5911002, 5920002,
		11001004, 12001004, 13001004, 14001005, 15001004,
		23111008, 23111009, 23111010,
		32111006, 33111003, 33111005,
		35111001, 35111009, 35111010, 35121009, 35121011,
	}
	now := time.Now()
	for _, id := range ids {
		h, ok := skills.Lookup(id)
		if !ok {
			t.Errorf("skill %d: not registered", id)
			continue
		}
		if h.Attack == nil || h.Attack.Summon == nil {
			t.Errorf("skill %d: missing Attack or Summon spec", id)
			continue
		}
		if id == 35121009 {
			continue
		}
		if h.Attack.Kind&combat.Summoned == 0 {
			t.Errorf("skill %d: Attack.Kind missing combat.Summoned", id)
			continue
		}
		def, err := content.SkillDefinition(wzRoot, id)
		if err != nil {
			t.Errorf("skill %d: SkillDefinition error: %v", id, err)
			continue
		}
		eff := h.Attack.Effect
		if eff.SkillID == 0 {
			eff, err = combat.SummonEffect(def, def.MaxLevel)
			if err != nil {
				t.Errorf("skill %d: SummonEffect(lv=%d) error: %v", id, def.MaxLevel, err)
				continue
			}
		}
		if id == 35121011 && (eff.Damage != 350 || eff.MobCount != 4) {
			t.Errorf("skill 35121011 WZ SummonEffect = %+v, want Damage=350 MobCount=4", eff)
		}
		levelSkill := id
		if h.Attack.LevelFrom != 0 {
			levelSkill = h.Attack.LevelFrom
		}
		job := id / 10000
		ch := domain.Character{
			ID: 1, Job: job, Level: 200, STR: 500, DEX: 500, INT: 500, LUK: 500,
			Skills: []domain.SkillState{{ID: levelSkill, Level: def.MaxLevel, MasterLevel: def.MaxLevel}},
		}
		stats := combat.PlayerStats{Job: job, Level: 200, STR: 500, DEX: 500, INT: 500, LUK: 500, WATK: 200, MATK: 200}
		targets := []combat.TargetInfo{{ObjectID: 1, Mob: content.Mob{ID: 9990000, Level: 10, HP: 1_000_000}, X: 0, Y: 0, HP: 1_000_000}}
		atk := protocol.Attack{
			Opcode: protocol.RecvSummonAttack, SkillID: id, TByte: 0x11, Tick: 1000,
			Targets: []protocol.AttackTarget{{ObjectID: 1, Lines: []protocol.AttackLine{{Raw: 1000, Damage: 1000}}}},
		}
		actx := combat.AttackContext{Kind: combat.Summoned, Character: ch, Stats: stats, Effect: eff, Attack: atk, Targets: targets, Now: now, SummonSkillID: id}
		plan, err := combat.PlanAttack(h.Attack, actx, func(int) bool { return false })
		if err != nil || len(plan.Targets) == 0 || len(plan.Targets[0].Lines) == 0 || plan.Targets[0].Lines[0] <= 0 {
			t.Errorf("skill %d: PlanAttack failed: err=%v plan=%+v", id, err, plan)
		}
	}
}
