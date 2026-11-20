package common_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/common"
)

func TestMonsterPotentialSkillsRegisteredAndApplyOnHitStatuses(t *testing.T) {
	allIDs := []int{
		skills.MonsterPotentialDeath,
		skills.MonsterPotentialStun,
		skills.MonsterPotentialSlow,
		skills.MonsterPotentialPoison,
		skills.MonsterPotentialDarkness,
		skills.MonsterPotentialSeal,
		skills.MonsterPotentialFreeze,
	}
	for _, id := range allIDs {
		if _, ok := skills.Lookup(id); !ok {
			t.Fatalf("monster potential skill %d is not registered", id)
		}
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Item.wz"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Character.wz", "Weapon"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Character.wz", "Weapon", "01302000.img.xml"),
		[]byte(`<imgdir name="01302000.img"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	optXML := `<imgdir name="ItemOption.img">
		<imgdir name="010221"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1000"/><int name="level" value="1"/><int name="prop" value="10"/></imgdir></imgdir></imgdir>
		<imgdir name="010226"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1001"/><int name="level" value="1"/><int name="prop" value="5"/></imgdir></imgdir></imgdir>
		<imgdir name="010231"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1002"/><int name="level" value="1"/><int name="prop" value="10"/></imgdir></imgdir></imgdir>
		<imgdir name="010236"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1003"/><int name="level" value="2"/><int name="prop" value="10"/></imgdir></imgdir></imgdir>
		<imgdir name="010241"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1004"/><int name="level" value="2"/><int name="prop" value="5"/></imgdir></imgdir></imgdir>
		<imgdir name="010246"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1005"/><int name="level" value="1"/><int name="prop" value="5"/></imgdir></imgdir></imgdir>
	</imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Item.wz", "ItemOption.img.xml"), []byte(optXML), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)

	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		optID     uint16
		wantStat  combat.MobStat
		wantSkill int32
		wantVal   int32
		poison    bool
	}{
		{10221, combat.MobPoison, 90001003, 0, true},
		{10226, combat.MobStun, 90001001, 1, false},
		{10231, combat.MobSpeed, 90001002, -50, false},
		{10236, combat.MobDarkness, 90001004, 80, false},
		{10241, combat.MobFreeze, 90001006, 1, false},
		{10246, combat.MobSeal, 90001005, 1, false},
	}
	for _, tc := range cases {
		c := domain.Character{
			ID: 100, Level: 120, Job: 112, STR: 300, DEX: 100,
			Equipment: []domain.Equipment{{
				ID: 1, Slot: -11, ItemID: 1302000, WATK: 100,
				Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{tc.optID}},
			}},
		}
		stats, err := skills.PlayerStatsChecked(c, now)
		if err != nil {
			t.Fatalf("opt %d PlayerStatsChecked: %v", tc.optID, err)
		}
		if stats.OnHitProcCount != 1 {
			t.Fatalf("opt %d: got %d OnHitProcs, want 1", tc.optID, stats.OnHitProcCount)
		}
		actx := combat.AttackContext{
			Kind:      combat.Melee,
			Character: c,
			Stats:     stats,
			Effect:    combat.Effect{SkillID: 0, Damage: 100, MobCount: 1, AttackCount: 1},
			Attack: protocol.Attack{
				Opcode: protocol.RecvMeleeAttack, SkillID: 0, TByte: 0x11, Speed: 6,
				Targets: []protocol.AttackTarget{{ObjectID: 1, Lines: []protocol.AttackLine{{Damage: 50, Raw: 50}}}},
			},
			Targets: []combat.TargetInfo{{ObjectID: 1, Mob: content.Mob{ID: 100100, HP: 50000}, HP: 50000}},
			Now:     now,
		}
		plan, err := combat.PlanAttack(&combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true}, actx, func(int) bool { return true })
		if err != nil {
			t.Fatalf("opt %d PlanAttack: %v", tc.optID, err)
		}
		if len(plan.Targets) != 1 || len(plan.Targets[0].Statuses) != 1 {
			t.Fatalf("opt %d: expected 1 applied status, got %+v", tc.optID, plan.Targets)
		}
		st := plan.Targets[0].Statuses[0]
		if st.Stat != tc.wantStat || st.SkillID != tc.wantSkill {
			t.Fatalf("opt %d: got stat=%v skill=%d, want stat=%v skill=%d", tc.optID, st.Stat, st.SkillID, tc.wantStat, tc.wantSkill)
		}
		if tc.poison {
			if st.TickDamage <= 0 || st.Interval != time.Second {
				t.Fatalf("opt %d poison: bad tick=%d interval=%v", tc.optID, st.TickDamage, st.Interval)
			}
		} else if st.Value != tc.wantVal {
			t.Fatalf("opt %d: got value=%d, want %d", tc.optID, st.Value, tc.wantVal)
		}
	}

	// Verify 90000000 instant-kill line acceptance on non-boss and clipping on boss.
	cDeath := domain.Character{
		ID: 101, Level: 120, Job: 112, STR: 50, DEX: 20,
		Skills: []domain.SkillState{{ID: skills.MonsterPotentialDeath, Level: 1}},
	}
	statsDeath, err := skills.PlayerStatsChecked(cDeath, now)
	if err != nil || !statsDeath.PotentialInstantKill {
		t.Fatalf("expected PotentialInstantKill=true, got %v err=%v", statsDeath.PotentialInstantKill, err)
	}
	normalMob := content.Mob{ID: 100100, HP: 500000}
	bossMob := content.Mob{ID: 8800000, HP: 500000, Boss: true}
	for _, tc := range []struct {
		name     string
		mob      content.Mob
		wantKill bool
	}{
		{"normal_mob", normalMob, true},
		{"boss_mob", bossMob, false},
	} {
		actx := combat.AttackContext{
			Kind:      combat.Melee,
			Character: cDeath,
			Stats:     statsDeath,
			Effect:    combat.Effect{SkillID: 0, Damage: 100, MobCount: 1, AttackCount: 1},
			Attack: protocol.Attack{
				Opcode: protocol.RecvMeleeAttack, SkillID: 0, TByte: 0x11, Speed: 6,
				Targets: []protocol.AttackTarget{{ObjectID: 1, Lines: []protocol.AttackLine{{Damage: 500000, Raw: 500000}}}},
			},
			Targets: []combat.TargetInfo{{ObjectID: 1, Mob: tc.mob, HP: tc.mob.HP}},
			Now:     now,
		}
		plan, err := combat.PlanAttack(&combat.AttackSpec{Kind: combat.Melee, IgnoreRange: true}, actx, func(int) bool { return true })
		if err != nil {
			t.Fatalf("%s: PlanAttack: %v", tc.name, err)
		}
		got := plan.Targets[0].Lines[0]
		if tc.wantKill && got != 500000 {
			t.Fatalf("%s: expected instant kill line 500000, got %d", tc.name, got)
		}
		if !tc.wantKill && got == 500000 {
			t.Fatalf("%s: boss should not be instant-killed", tc.name)
		}
	}
}
