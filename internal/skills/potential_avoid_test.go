package skills

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func potentialAvoidFixture(t *testing.T) (domain.Character, time.Time) {
	t.Helper()
	c, now := potentialStatsFixture(t)
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	p := filepath.Join(root, "Item.wz/ItemOption.img.xml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	extra := `<imgdir name="010008"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="incEVA" value="5"/></imgdir></imgdir></imgdir><imgdir name="010048"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="incEVAr" value="10"/></imgdir></imgdir></imgdir>`
	text := string(b)
	text = strings.TrimSuffix(text, "</imgdir>") + extra + "</imgdir>"
	if err = os.WriteFile(p, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "Skill.wz/400.img.xml"), []byte(`<imgdir name="book"><imgdir name="skill"><imgdir name="4000000"><imgdir name="common"><int name="maxLevel" value="20"/><string name="y" value="8*x"/></imgdir></imgdir></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	// Isolated registration fixture for the existing flat Passive vocabulary;
	// actual original potential rows are tested independently below.
	registry.Lock()
	old, present := registry.byID[4000000]
	registry.byID[4000000] = Handler{ID: 4000000, Kind: KindPassive, Passive: func(p *PassiveContext) Bonus { return Bonus{AVOID: p.Values["y"]} }}
	registry.Unlock()
	t.Cleanup(func() {
		registry.Lock()
		defer registry.Unlock()
		if present {
			registry.byID[4000000] = old
		} else {
			delete(registry.byID, 4000000)
		}
	})
	c.Job = 400
	c.Skills = []domain.SkillState{{ID: 4000000, Level: 2}}
	c.Equipment[0].AVOID = 10
	c.Equipment[0].Potential.Options = [5]uint16{10008, 10048}
	c.SkillBuffs = []domain.SkillBuff{{Stat: "AVOID", Value: 7, ExpiresAt: now.Add(time.Minute)}, {Stat: "ONYX_SHROUD", Value: 11, ExpiresAt: now.Add(time.Minute)}}
	return c, now
}
func TestPotentialAvoidFlatPercentAndChanceAreIndependent(t *testing.T) {
	c, now := potentialAvoidFixture(t)
	before := c
	before.Equipment = append([]domain.Equipment(nil), c.Equipment...)
	before.Skills = append([]domain.SkillState(nil), c.Skills...)
	before.SkillBuffs = append([]domain.SkillBuff(nil), c.SkillBuffs...)
	b, _, err := PotentialBonusesChecked(c, now)
	if err != nil || b.AVOID != 5 || b.AVOIDPercent != 10 || b.AvoidRate != 0 {
		t.Fatal("potential flat/percent became chance", b, err)
	}
	p, err := PlayerStatsChecked(c, now)
	if err != nil || p.Avoid != 38 || p.PotentialAvoidPercent != 10 || p.DodgeRate != 11 {
		t.Fatal("flat source missing/percent applied to incomplete base/chance changed", p, err)
	}
	c.Equipment[0].Potential.Options = [5]uint16{10008, 10008, 10008, 10008, 10008}
	p, err = PlayerStatsChecked(c, now)
	if err != nil || p.Avoid != 58 || p.PotentialAvoidPercent != 0 || p.DodgeRate != 11 {
		t.Fatal("five flat sources not added exactlyonce", p, err)
	}
	c.Equipment[0].Potential.Options = [5]uint16{10048}
	p, err = PlayerStatsChecked(c, now)
	if err != nil || p.Avoid != 33 || p.PotentialAvoidPercent != 10 || p.DodgeRate != 11 {
		t.Fatal("percentage invented flat value or chance", p, err)
	}
	c.Equipment[0].Potential.Options = before.Equipment[0].Potential.Options
	if !reflect.DeepEqual(before, c) {
		t.Fatal("view mutated HPMP/base/source metadata")
	}
}
func TestPotentialAvoidInactiveSourcesKeepLegacyView(t *testing.T) {
	c, now := potentialAvoidFixture(t)
	for _, kind := range []string{"absent", "hidden", "bag", "cash", "expired"} {
		copy := c
		copy.Equipment = append([]domain.Equipment(nil), c.Equipment...)
		switch kind {
		case "absent":
			copy.Equipment[0].Potential = domain.EquipmentPotential{}
		case "hidden":
			copy.Equipment[0].Potential.RawState = 1
		case "bag":
			copy.Equipment[0].Slot = 1
		case "cash":
			copy.Equipment[0].CashID = 5
		case "expired":
			copy.Equipment[0].ExpiresAt = now.UnixMilli()
		}
		got, err := PlayerStatsChecked(copy, now)
		got.ResourceCosts = content.ResourceCostModifiers{}
		if err != nil || got != legacyPlayerStats(copy, now) || got.PotentialAvoidPercent != 0 {
			t.Fatal(kind, got, err)
		}
	}
	c.Equipment[0].Potential.RawState = 0x15
	if _, err := PlayerStatsChecked(c, now); err == nil {
		t.Fatal("unknown tier became a numeric view")
	}
}
func TestPotentialAvoidOriginalWZFiveFlatRows(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "MapleStory_Legacy_Server", "wz"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "Item.wz/ItemOption.img.xml")); err != nil {
		t.Skip("original WZ absent")
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	now := time.Now()
	for _, tc := range []struct {
		id   uint16
		flat int
	}{{8, 2}, {10008, 4}, {20008, 4}, {30008, 6}, {40008, 8}} {
		c := domain.Character{Job: 100, Level: 30, STR: 10, DEX: 10, INT: 10, LUK: 10, Equipment: []domain.Equipment{{ItemID: 1302000, Slot: -11, AVOID: 10, Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{tc.id}}}}, SkillBuffs: []domain.SkillBuff{{Stat: "AVOID", Value: 3, ExpiresAt: now.Add(time.Minute)}}}
		p, err := PlayerStatsChecked(c, now)
		if err != nil || p.Avoid != 13+tc.flat || p.PotentialAvoidPercent != 0 || p.DodgeRate != 0 {
			t.Fatal("originalincEVA", tc, p, err)
		}
	}
	for _, tc := range []struct {
		id      uint16
		percent int
	}{{10048, 1}, {20048, 2}, {30048, 3}, {40048, 6}} {
		c := domain.Character{Job: 100, Level: 30, STR: 10, DEX: 10, INT: 10, LUK: 10, Equipment: []domain.Equipment{{ItemID: 1302000, Slot: -11, AVOID: 10, Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{tc.id}}}}}
		p, err := PlayerStatsChecked(c, now)
		if err != nil || p.Avoid != 10 || p.PotentialAvoidPercent != tc.percent || p.DodgeRate != 0 {
			t.Fatal("originalincEVAr became chance or incomplete-base multiplier", tc, p, err)
		}
	}
}
