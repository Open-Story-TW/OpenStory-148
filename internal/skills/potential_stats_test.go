package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func potentialStatsFixture(t *testing.T) (domain.Character, time.Time) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"Item.wz", "Character.wz/Weapon", "Skill.wz"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	var xml strings.Builder
	xml.WriteString(`<imgdir name="ItemOption.img">`)
	for _, row := range []struct {
		id     int
		fields string
	}{
		{10001, `<int name="incSTR" value="5"/><int name="incSTRr" value="10"/><int name="incMHP" value="10"/><int name="incMHPr" value="10"/><int name="incPAD" value="3"/><int name="incPADr" value="20"/><int name="incACC" value="2"/><int name="incACCr" value="10"/><int name="incCr" value="5"/><int name="incCriticaldamageMax" value="10"/><int name="ignoreTargetDEF" value="20"/><int name="incPDD" value="4"/><int name="incPDDr" value="10"/><int name="incSpeed" value="3"/>`},
		{10002, `<int name="incSTRr" value="20"/><int name="incMHPr" value="20"/><int name="incMMP" value="7"/><int name="incMMPr" value="50"/><int name="incDAMr" value="10"/>`},
		{30001, `<int name="incDAMr" value="30"/><int name="boss" value="1"/>`},
		{20001, `<int name="HP" value="5"/><int name="prop" value="20"/>`},
		{20002, `<int name="incAllskill" value="1"/>`},
		{10003, `<int name="incINT" value="5"/><int name="incINTr" value="20"/><int name="incMADr" value="10"/>`},
	} {
		fmt.Fprintf(&xml, `<imgdir name="%06d"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level">`, row.id)
		for level := 1; level <= 20; level++ {
			fmt.Fprintf(&xml, `<imgdir name="%d">%s</imgdir>`, level, row.fields)
		}
		xml.WriteString(`</imgdir></imgdir>`)
	}
	xml.WriteString(`</imgdir>`)
	if err := os.WriteFile(filepath.Join(root, "Item.wz/ItemOption.img.xml"), []byte(xml.String()), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Character.wz/Weapon/01302000.img.xml"), []byte(`<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Skill.wz/100.img.xml"), []byte(`<imgdir name="100.img"><imgdir name="skill"><imgdir name="1001004"><imgdir name="common"><int name="maxLevel" value="20"/><string name="mpCon" value="3+2*u(x/5)"/><string name="damage" value="180+5*x"/></imgdir></imgdir><imgdir name="1001005"><imgdir name="common"><int name="maxLevel" value="20"/><string name="mpCon" value="6+2*d(x/5)"/><string name="damage" value="120+5*x"/><int name="mobCount" value="6"/><int name="range" value="180"/></imgdir></imgdir></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	now := time.Unix(1800000000, 0)
	c := domain.Character{Job: 100, Level: 30, STR: 100, DEX: 10, INT: 4, LUK: 4, VitalsModel: 1, BaseMaxHP: 100, BaseMaxMP: 50, HP: 30, MP: 20,
		Equipment: []domain.Equipment{{ID: 1, ItemID: 1302000, Slot: -11, STR: 20, WATK: 10, ACC: 5, HP: 20, MP: 5, WDEF: 10,
			Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{10001, 10002}}}}}
	return c, now
}

func TestNativeContactPotentialKeepsCheckedCombatStatsAndDoesNotReduceTwice(t *testing.T) {
	c, now := potentialStatsFixture(t)
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	p := filepath.Join(root, "Item.wz/ItemOption.img.xml")
	xml, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	rows := `<imgdir name="020353"><imgdir name="info"><int name="weight" value="5"/></imgdir><imgdir name="level"><imgdir name="1"><int name="prop" value="30"/><int name="ignoreDAM" value="7"/></imgdir></imgdir></imgdir>` +
		`<imgdir name="010014"><imgdir name="info"><int name="weight" value="30"/></imgdir><imgdir name="level"><imgdir name="1"><int name="incMDD" value="10"/></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(p, []byte(strings.TrimSuffix(string(xml), "</imgdir>")+rows+"</imgdir>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Character.wz/Cap"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Character.wz/Cap/01002019.img.xml"), []byte(`<imgdir name="hat"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	c.Equipment[0].Potential = domain.EquipmentPotential{}
	c.Equipment = append(c.Equipment, domain.Equipment{ID: 86996, ItemID: 1002019, Slot: -1, WDEF: 8,
		Potential: domain.EquipmentPotential{Present: true, RawState: 18, Flags: 65535, Options: [5]uint16{10014}}})
	baseline, err := PlayerStatsChecked(c, now)
	if err != nil {
		t.Fatal(err)
	}
	c.Equipment[1].Potential.Options = [5]uint16{20353, 10014}
	got, err := PlayerStatsChecked(c, now)
	if err != nil || !reflect.DeepEqual(got, baseline) {
		t.Fatalf("native contact line broke checked attack/defense stats: %+v %v", got, err)
	}
	b, effects, err := PotentialBonusesChecked(c, now)
	if err != nil || b.MDEF != 10 || len(effects.NativeContactDamage) != 1 || len(effects.Pending) != 0 {
		t.Fatalf("actual rolled sources not resolved: %+v %+v %v", b, effects, err)
	}
	// This input is the native result after its own potential calculation.
	// Server-side HP/MP processing must not draw again or subtract another 7.
	if hp, mp := TakenDamage(c, 12, now); hp != 12 || mp != 0 {
		t.Fatalf("native final hit reduced twice: HP=%d MP=%d", hp, mp)
	}
}

func TestPotentialSharedLegacyBoundsUseTheCheckedCombatView(t *testing.T) {
	c, now := potentialStatsFixture(t)
	c.Skills = []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}}
	stats, err := PlayerStatsChecked(c, now)
	if err != nil {
		t.Fatal(err)
	}
	mob := content.Mob{HP: 1000, Level: 1, PDRate: 50}
	for _, id := range []int{0, 1001004, 1001005} {
		profile, err := content.PhysicalAttackProfileFor(os.Getenv("MAPLEGO_WZ_ROOT"), c, id, now)
		if err != nil {
			t.Fatal(err)
		}
		got, err := profile.MaximumDamage(mob)
		want := combat.LineCeiling(combat.DamageContext{Stats: stats, Mob: mob, Effect: combat.Effect{SkillID: id, Damage: profile.DamagePercent}})
		if err != nil || got != want || got <= 0 {
			t.Fatal("legacy content/checked generic mismatch", id, got, want, err)
		}
		if id == 0 {
			normal, err := content.NormalAttackMaximum(os.Getenv("MAPLEGO_WZ_ROOT"), c, mob, now)
			if err != nil || normal != want {
				t.Fatal("normal Store/world shared helper differs", normal, want, err)
			}
		}
	}
	c.Equipment[0].Potential.Options = [5]uint16{20001}
	if _, err := content.PrepareNormalAttackMaximum(os.Getenv("MAPLEGO_WZ_ROOT"), c, now); err == nil {
		t.Fatal("legacy pending source became old raw bound")
	}
}

func TestPotentialStatsAttackAndVitalsAreAuthoritativeWithoutHealing(t *testing.T) {
	c, now := potentialStatsFixture(t)
	c.Skills = []domain.SkillState{{ID: 1000006, Level: 1}}
	c.SkillBuffs = []domain.SkillBuff{
		{Stat: "MAPLE_WARRIOR", Value: 10, ExpiresAt: now.Add(time.Minute)},
		{Stat: "STR", Value: 7, ExpiresAt: now.Add(time.Minute)},
		{Stat: "WATK", Value: 5, ExpiresAt: now.Add(time.Minute)},
		{Stat: "MAXHP", Value: 60, ExpiresAt: now.Add(time.Minute)},
	}
	before := c
	before.Equipment = append([]domain.Equipment(nil), c.Equipment...)
	before.Skills = append([]domain.SkillState(nil), c.Skills...)
	before.SkillBuffs = append([]domain.SkillBuff(nil), c.SkillBuffs...)
	p, err := PlayerStatsChecked(c, now)
	if err != nil || p.STR != 179 || p.WATK != 20 || p.CritRate != 5 || p.CritDamage != 10 || p.IgnoreDefense != 20 || p.Speed != 103 || p.WDEF <= 10 {
		t.Fatal(p, err)
	}
	hp, mp, err := content.DerivedVitals(c, now)
	if err != nil || hp != 249 || mp != 93 {
		t.Fatalf("flat+passive+potential+HB one-floor got%d/%d err%v", hp, mp, err)
	}
	if !reflect.DeepEqual(c, before) || c.HP != 30 || c.MP != 20 || c.BaseMaxHP != 100 || c.BaseMaxMP != 50 {
		t.Fatal("derivation healed or changed source/base")
	}
	c.Job = 3112
	if _, mp, err = content.DerivedVitals(c, now); err != nil || mp != 120 {
		t.Fatal("potential created mana instead of DemonFury", mp, err)
	}
}

func TestPotentialStatsLegacyExclusionCheckedErrorsAndFiveLines(t *testing.T) {
	c, now := potentialStatsFixture(t)
	for name, change := range map[string]func(*domain.Equipment){
		"absent":  func(e *domain.Equipment) { e.Potential = domain.EquipmentPotential{} },
		"hidden":  func(e *domain.Equipment) { e.Potential.RawState = 1 },
		"bag":     func(e *domain.Equipment) { e.Slot = 1 },
		"cash":    func(e *domain.Equipment) { e.CashID = 5 },
		"expired": func(e *domain.Equipment) { e.ExpiresAt = now.UnixMilli() },
	} {
		t.Run(name, func(t *testing.T) {
			copy := c
			copy.Equipment = append([]domain.Equipment(nil), c.Equipment...)
			change(&copy.Equipment[0])
			got, err := PlayerStatsChecked(copy, now)
			// The new immutable resource snapshot is metadata for the cost
			// consumer; existing scalar no-potential stats stay byte-for-byte.
			got.ResourceCosts = content.ResourceCostModifiers{}
			if err != nil || got != legacyPlayerStats(copy, now) {
				t.Fatal("nonactive potential changed existing view", got, err)
			}
		})
	}
	c.Equipment[0].Potential.Options = [5]uint16{10001, 10001, 10001, 10001, 10001}
	p, err := PlayerStatsChecked(c, now)
	if err != nil || p.STR != 217 {
		t.Fatal("all5 lines not counted exactly once", p, err)
	} // (120+25)*1.5 floor=217
	c.Equipment[0].Potential.Options = [5]uint16{20001}
	_, err = PlayerStatsChecked(c, now)
	var pending *content.PendingPotentialEffectsError
	if !errors.As(err, &pending) || len(pending.Effects) != 1 || PlayerStats(c, now).STR >= 0 {
		t.Fatal("pending event became equipment-only success", err)
	}
	c.Equipment[0].Potential.Options = [5]uint16{65519}
	if _, err = PlayerStatsChecked(c, now); err == nil {
		t.Fatal("unknown revealed option silently disappeared")
	}
	c.Equipment[0].Potential.Options = [5]uint16{20002}
	if _, _, err = content.DerivedVitals(c, now); !errors.As(err, &pending) {
		t.Fatal("unimplemented all-skill effect silently changed passive maxima", err)
	}
}

func TestPotentialMagicINTAndMultipliersReachCombatCeilings(t *testing.T) {
	c, now := potentialStatsFixture(t)
	c.INT = 100
	c.Equipment[0].INT = 20
	c.Equipment[0].MATK = 10
	c.Equipment[0].Potential.Options = [5]uint16{10003}
	p, err := PlayerStatsChecked(c, now)
	if err != nil || p.INT != 165 || p.MATK != 11 || p.Accuracy != 159 {
		t.Fatal("INT potential% then magic% ordering", p, err)
	} // (100+20+5)*1.2=150 then+15
	c.Equipment[0].Potential.Options = [5]uint16{10002, 10002, 30001}
	p, err = PlayerStatsChecked(c, now)
	if err != nil {
		t.Fatal(err)
	}
	dc := combat.DamageContext{Stats: p, Effect: combat.NormalEffect(), Mob: content.Mob{HP: 9999, Level: 1}}
	_, normal := combat.LineBounds(dc)
	without := dc
	without.Stats.PotentialDamageMultiplier = 0
	without.Stats.PotentialBossDamageMultiplier = 0
	_, plain := combat.LineBounds(without)
	dc.Mob.Boss = true
	_, boss := combat.LineBounds(dc)
	if normal <= plain || boss <= normal || p.PotentialDamageMultiplier < 1.20 || p.PotentialBossDamageMultiplier < 1.57 {
		t.Fatal("multipliers only appeared in a report", plain, normal, boss, p)
	}
	crit := combat.CritCeiling(combat.DamageContext{Stats: combat.PlayerStats{CritDamage: 10}}, 100)
	if crit != 160 {
		t.Fatal("critical max missing from actual bound", crit)
	}
}

func TestInfinityCheckedPotentialAndLegacyPathsBothScaleMagicAndPreWarriorINT(t *testing.T) {
	c, now := potentialStatsFixture(t)
	c.INT = 100
	c.Equipment[0].MATK = 20
	c.Equipment[0].Potential.Options = [5]uint16{10003}
	c.SkillBuffs = []domain.SkillBuff{{ID: 2121000, Stat: "MAPLE_WARRIOR", Value: 15, ExpiresAt: now.Add(time.Minute)}}
	before, err := PlayerStatsChecked(c, now)
	if err != nil {
		t.Fatal(err)
	}
	c.SkillBuffs = append(c.SkillBuffs, domain.SkillBuff{ID: 2121004, Stat: "INFINITY", Value: 21, ExpiresAt: now.Add(time.Minute)})
	after, err := PlayerStatsChecked(c, now)
	if err != nil {
		t.Fatal(err)
	}
	if before.INT != 153 || before.MATK != 22 || after.INT != 178 || after.MATK != 26 || !after.ResourceCosts.Infinity {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	c.SkillBuffs[1].ExpiresAt = now
	expired, err := PlayerStatsChecked(c, now)
	if err != nil || expired.INT != before.INT || expired.MATK != before.MATK || expired.ResourceCosts.Infinity {
		t.Fatalf("expired %+v %v", expired, err)
	}
}
