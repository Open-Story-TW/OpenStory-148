package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func effectsFixture(t *testing.T, flat int) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"Item.wz", "Character.wz/Weapon"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	xml := `<imgdir name="ItemOption.img">`
	for _, row := range []struct {
		id     int
		fields string
	}{
		{10001, fmt.Sprintf(`<int name="incSTR" value="%d"/><int name="incMHP" value="10"/><int name="incMHPr" value="10"/>`, flat)},
		{10002, `<int name="HP" value="5"/><int name="prop" value="20"/>`},
		{60003, `<int name="incDAMr" value="40"/><int name="boss" value="1"/>`},
		{10003, `<int name="futureUnsupportedField" value="1"/>`},
	} {
		xml += fmt.Sprintf(`<imgdir name="%06d"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level">`, row.id)
		for level := 1; level <= 20; level++ {
			xml += fmt.Sprintf(`<imgdir name="%d">%s</imgdir>`, level, row.fields)
		}
		xml += `</imgdir></imgdir>`
	}
	xml += `</imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Item.wz/ItemOption.img.xml"), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1302000, 1302001} {
		cash := ""
		if id == 1302001 {
			cash = `<int name="cash" value="1"/>`
		}
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("Character.wz/Weapon/%08d.img.xml", id)), []byte(`<imgdir name="weapon"><imgdir name="info"><int name="reqLevel" value="10"/>`+cash+`</imgdir></imgdir>`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func revealedEffectsGear(option uint16) domain.Equipment {
	return domain.Equipment{ID: 1, ItemID: 1302000, Slot: -11, Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{option}}}
}

func TestPotentialEffectsWornRevealCashExpiryAndUnknownData(t *testing.T) {
	root := effectsFixture(t, 5)
	now := time.Unix(1800000000, 0)
	e := revealedEffectsGear(10001)
	got, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{e}}, now)
	if err != nil || got.Values["incSTR"] != 5 || len(got.Lines) != 1 || got.Lines[0].Band != 1 || len(got.Pending) != 0 {
		t.Fatal(got, err)
	}
	for name, change := range map[string]func(*domain.Equipment){
		"bag": func(e *domain.Equipment) { e.Slot = 1 }, "cash appearance": func(e *domain.Equipment) { e.Slot = -111 },
		"cash serial": func(e *domain.Equipment) { e.CashID = 1 }, "cash template": func(e *domain.Equipment) { e.ItemID = 1302001 },
		"hidden": func(e *domain.Equipment) { e.Potential.RawState = 1 }, "expired": func(e *domain.Equipment) { e.ExpiresAt = now.UnixMilli() },
	} {
		t.Run(name, func(t *testing.T) {
			copy := e
			change(&copy)
			got, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{copy}}, now)
			if err != nil || len(got.Lines) != 0 || got.Values["incSTR"] != 0 {
				t.Fatal(got, err)
			}
		})
	}
	for _, state := range []uint8{0x10, 0x15, 0x1f} {
		copy := e
		copy.Potential.RawState = state
		if _, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{copy}}, now); err == nil {
			t.Fatal("unknown revealed state became legacy", state)
		}
		for _, exclude := range []func(*domain.Equipment){
			func(e *domain.Equipment) { e.ItemID = 1302001 },
			func(e *domain.Equipment) { e.Slot = 1 },
			func(e *domain.Equipment) { e.ExpiresAt = now.UnixMilli() },
		} {
			excluded := copy
			exclude(&excluded)
			got, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{excluded}}, now)
			if err != nil || len(got.Lines) != 0 {
				t.Fatal("excluded source became active", state, err)
			}
		}
	}

	for _, id := range []uint16{12345, 65519, 10003} {
		copy := e
		copy.Potential.Options[0] = id
		if _, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{copy}}, now); err == nil {
			t.Fatal("unknown source became zero", id)
		}
	}
	copy := e
	copy.Potential.Options = [5]uint16{60003, 10002}
	copy.Potential.RawState = 20
	got, err = ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{copy}}, now)
	if err != nil || len(got.Pending) != 1 || got.Lines[0].OptionID != 60003 || len(got.BossDamagePercents) != 1 || len(got.DamagePercents) != 0 {
		t.Fatal(got, err)
	}
	if _, err := ResolvePotentialEffects(root, domain.Character{Equipment: []domain.Equipment{e, e}}, now); err == nil {
		t.Fatal("duplicate source counted twice")
	}
}

func TestPotentialEffectsCacheSingleSourceDetachedAndRootIsolated(t *testing.T) {
	root := effectsFixture(t, 5)
	c := domain.Character{Equipment: []domain.Equipment{revealedEffectsGear(10001)}}
	now := time.Now()
	first, err := ResolvePotentialEffects(root, c, now)
	if err != nil {
		t.Fatal(err)
	}
	first.Values["incSTR"] = 999
	first.Lines[0].Attributes.Values["incSTR"] = 999
	p := filepath.Join(root, "Item.wz/ItemOption.img.xml")
	data, _ := os.ReadFile(p)
	data = []byte(strings.ReplaceAll(string(data), `name="incSTR" value="5"`, `name="incSTR" value="9"`))
	if err = os.WriteFile(p, data, 0644); err != nil {
		t.Fatal(err)
	}
	again, err := ResolvePotentialEffects(root, c, now)
	if err != nil || again.Values["incSTR"] != 5 || again.Lines[0].Attributes.Values["incSTR"] != 5 || again.SourceSHA256 != first.SourceSHA256 {
		t.Fatal("cache changed or aliased", again, err)
	}
	other, err := ResolvePotentialEffects(effectsFixture(t, 7), c, now)
	if err != nil || other.Values["incSTR"] != 7 || other.SourceSHA256 == again.SourceSHA256 {
		t.Fatal("root cache leaked", other, err)
	}
	coverage := PotentialAttributeSupport()
	coverage["incSTR"] = PotentialPending
	if PotentialAttributeSupport()["incSTR"] != PotentialNumeric {
		t.Fatal("coverage map aliases shared state")
	}
}

func TestPotentialEffectsOriginal206RowsHaveExplicitCoverage(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("set original WZ root for full206 effect coverage")
	}
	c, err := LoadPotentialCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	pendingRows := 0
	for _, id := range c.IDs() {
		o, _ := c.Option(id)
		pending := false
		for _, level := range o.Levels {
			for key := range level.Values {
				seen[key] = true
				if potentialEffectSupport[key] == "" {
					t.Fatal("unclassified numeric", id, key)
				}
				pending = pending || potentialEffectSupport[key] == PotentialPending
			}
			for key := range level.Text {
				seen[key] = true
				if potentialEffectSupport[key] == "" {
					t.Fatal("unclassified text", id, key)
				}
				pending = pending || potentialEffectSupport[key] == PotentialPending
			}
		}
		if pending {
			pendingRows++
		}
	}
	if len(c.IDs()) != 206 || len(seen) != 52 || pendingRows != 50 {
		t.Fatal("coverage inventory changed", len(c.IDs()), len(seen), pendingRows)
	}
	var baseline struct {
		Source struct {
			SHA256 string `json:"sha256"`
		} `json:"source"`
		Support map[string]PotentialEffectSupport `json:"attribute_support"`
		Rows    []struct {
			OptionID   int      `json:"option_id"`
			Attributes []string `json:"attributes"`
			Pending    bool     `json:"pending_runtime"`
		} `json:"rows"`
	}
	data, err := os.ReadFile("testdata/potential-effective-stats.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline.Source.SHA256 != c.sha256 || !reflect.DeepEqual(baseline.Support, PotentialAttributeSupport()) || len(baseline.Rows) != len(c.IDs()) {
		t.Fatal("source coverage baseline drift")
	}
	for _, row := range baseline.Rows {
		option, ok := c.Option(row.OptionID)
		if !ok {
			t.Fatal("missing baseline option", row.OptionID)
		}
		actual := map[string]bool{}
		pending := false
		for _, level := range option.Levels {
			for key := range level.Values {
				actual[key] = true
				pending = pending || potentialEffectSupport[key] == PotentialPending
			}
			for key := range level.Text {
				actual[key] = true
				pending = pending || potentialEffectSupport[key] == PotentialPending
			}
		}
		if len(actual) != len(row.Attributes) || pending != row.Pending {
			t.Fatal("coverage row drift", row.OptionID)
		}
		for _, key := range row.Attributes {
			if !actual[key] {
				t.Fatal("coverage attribute drift", row.OptionID, key)
			}
		}
	}
	t.Log("explicit source coverage", len(c.IDs()), "options", len(seen), "attributes; pending runtime rows", pendingRows)
}

func TestPotentialPhysicalProviderMissingCannotRestoreLegacySuccess(t *testing.T) {
	root := effectsFixture(t, 5)
	c := domain.Character{HP: 50, Equipment: []domain.Equipment{revealedEffectsGear(10001)}}
	before := PotentialPhysicalBoundsProvider
	t.Cleanup(func() { PotentialPhysicalBoundsProvider = before })
	PotentialPhysicalBoundsProvider = nil
	if _, err := PreparePotentialPhysicalMaximum(root, c, time.Now()); err == nil {
		t.Fatal("active source trusted without a checked provider")
	}
	PotentialPhysicalBoundsProvider = func(domain.Character, time.Time) (PotentialPhysicalMaximum, error) { return nil, nil }
	if _, err := PreparePotentialPhysicalMaximum(root, c, time.Now()); err == nil {
		t.Fatal("empty checked bound became compatibility fallback")
	}
	c.Equipment[0].Potential = domain.EquipmentPotential{}
	if maximum, err := PreparePotentialPhysicalMaximum(root, c, time.Now()); err != nil || maximum != nil {
		t.Fatal("legacy absence requires no new provider", err)
	}
}
