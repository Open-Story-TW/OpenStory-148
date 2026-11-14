package content

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const potentialSourceFixture = `<imgdir name="ItemOption.img">
<imgdir name="060003"><imgdir name="info"><int name="weight" value="2"/><string name="string" value="Boss damage : +#incDAMr%"/></imgdir><imgdir name="level"><imgdir name="1"><int name="boss" value="1"/><int name="incDAMr" value="40"/></imgdir><imgdir name="20"><int name="boss" value="1"/><int name="incDAMr" value="30"/></imgdir></imgdir></imgdir>
<imgdir name="031004"><imgdir name="info"><int name="optionType" value="53"/><int name="reqLevel" value="70"/><int name="weight" value="5"/><string name="string" value="Decent Hyper Body"/></imgdir><imgdir name="level"><imgdir name="1"><int name="level" value="1"/><int name="futureEffect" value="-2"/><string name="face" value="cheers"/></imgdir></imgdir></imgdir>
</imgdir>`

func TestPotentialCatalogRetainsAllSourceEffectsAndDetachedLookups(t *testing.T) {
	c, err := parsePotentialCatalog([]byte(potentialSourceFixture))
	if err != nil {
		t.Fatal(err)
	}
	ids := c.IDs()
	if len(ids) != 2 || ids[0] != 31004 || ids[1] != 60003 {
		t.Fatal("source IDs were filtered or confused with entry positions", ids)
	}
	ids[0] = 999
	hash := sha256.Sum256([]byte(potentialSourceFixture))
	if c.SourceSHA256() != hex.EncodeToString(hash[:]) {
		t.Fatal("catalog hash does not identify the exact bytes parsed")
	}
	o, ok := c.Option(31004)
	if !ok || o.RequiredLevel != 70 || o.OptionType != 53 || o.Weight != 5 || o.Levels[1].Values["futureEffect"] != -2 || o.Levels[1].Text["face"] != "cheers" {
		t.Fatal("special/unknown option data was lost", o)
	}
	o.Info.Values["weight"] = 999
	o.Levels[1].Values["futureEffect"] = 999
	o.Levels[1].Text["face"] = "changed"
	delete(o.Levels, 1)
	again, _ := c.Option(31004)
	if again.Info.Values["weight"] != 5 || again.Levels[1].Values["futureEffect"] != -2 || again.Levels[1].Text["face"] != "cheers" || c.IDs()[0] != 31004 {
		t.Fatal("caller mutated the shared source catalog")
	}
	level, ok := c.Level(60003, 20)
	if !ok || level.Values["incDAMr"] != 30 || level.Values["boss"] != 1 {
		t.Fatal("explicit band data was changed or extrapolated", level)
	}
	level.Values["incDAMr"] = 999
	if level, _ = c.Level(60003, 20); level.Values["incDAMr"] != 30 {
		t.Fatal("level lookup leaked an internal map")
	}
	if _, ok = c.Level(60003, 2); ok {
		t.Fatal("missing level was silently extrapolated")
	}
	if _, ok = c.Option(12345); ok {
		t.Fatal("unknown option invented")
	}
}

func TestPotentialCatalogRejectsAmbiguousOrCorruptedWZ(t *testing.T) {
	cases := map[string]string{
		"duplicate ID":         strings.Replace(potentialSourceFixture, `name="031004"`, `name="60003"`, 1),
		"duplicate band":       strings.Replace(potentialSourceFixture, `name="20"`, `name="01"`, 1),
		"duplicate attribute":  strings.Replace(potentialSourceFixture, `<int name="boss" value="1"/>`, `<int name="boss" value="1"/><int name="boss" value="1"/>`, 1),
		"invalid integer":      strings.Replace(potentialSourceFixture, `value="70"`, `value="bad"`, 1),
		"wrong metadata type":  strings.Replace(potentialSourceFixture, `<int name="reqLevel" value="70"/>`, `<string name="reqLevel" value="70"/>`, 1),
		"wire ID overflow":     strings.Replace(potentialSourceFixture, `name="060003"`, `name="65536"`, 1),
		"unsupported link":     strings.Replace(potentialSourceFixture, `<int name="boss" value="1"/>`, `<uol name="boss" value="../../boss"/>`, 1),
		"numeric overflow":     strings.Replace(potentialSourceFixture, `value="40"`, `value="2147483648"`, 1),
		"missing weight":       strings.Replace(potentialSourceFixture, `<int name="weight" value="2"/>`, ``, 1),
		"wrong band container": strings.Replace(strings.Replace(potentialSourceFixture, `<imgdir name="level">`, `<int name="level">`, 1), `</imgdir></imgdir></imgdir>`, `</imgdir></int></imgdir>`, 1),
		"invalid root":         strings.Replace(potentialSourceFixture, `name="ItemOption.img"`, `name="Other.img"`, 1),
		"second document":      potentialSourceFixture + `<imgdir name="ItemOption.img"/>`,
		"truncated":            potentialSourceFixture[:len(potentialSourceFixture)-8],
		"empty":                `<imgdir name="ItemOption.img"/>`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePotentialCatalog([]byte(source)); err == nil {
				t.Fatal("ambiguous source accepted")
			}
		})
	}
}

func TestPotentialCatalogLoadRequiresRealFile(t *testing.T) {
	if _, err := LoadPotentialCatalog(""); err == nil {
		t.Fatal("fake embedded potential catalog supplied")
	}
	root := t.TempDir()
	if _, err := LoadPotentialCatalog(root); err == nil {
		t.Fatal("missing source accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, "Item.wz"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Item.wz", "ItemOption.img.xml"), []byte(potentialSourceFixture), 0644); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadPotentialCatalog(root); err != nil || len(c.IDs()) != 2 {
		t.Fatal("real source failed", err)
	}
}

func TestPotentialCatalogAuditedTMS145WZ(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("set MAPLEGO_WZ_ROOT for the original TMS145 source audit")
	}
	c, err := LoadPotentialCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.IDs()) != 206 {
		t.Fatal("source inventory changed; audit all added/removed options", len(c.IDs()))
	}
	if c.SourceSHA256() != "67ab77bf05353362508afbc8651a8a03d9b8eabf515c62f341d9eecbb3b03ffe" {
		t.Fatal("original ItemOption source changed; re-audit all option data", c.SourceSHA256())
	}
	for _, id := range c.IDs() {
		o, _ := c.Option(id)
		if len(o.Levels) != 20 {
			t.Fatalf("option %d lost a source band", id)
		}
		for band := 1; band <= 20; band++ {
			if _, ok := c.Level(id, band); !ok {
				t.Fatal("source band missing", id, band)
			}
		}
	}
	for _, tc := range []struct {
		id, band int
		key      string
		value    int
	}{
		{1, 1, "incSTR", 1}, {1, 20, "incSTR", 6}, {10041, 20, "incSTRr", 3},
		{31004, 1, "level", 1}, {60003, 1, "incDAMr", 40}, {60003, 20, "incDAMr", 30},
	} {
		t.Run(fmt.Sprintf("%d-band%d", tc.id, tc.band), func(t *testing.T) {
			level, ok := c.Level(tc.id, tc.band)
			if !ok || level.Values[tc.key] != tc.value {
				t.Fatal("original WZ value changed", level)
			}
		})
	}
	t.Log("full original catalog:", len(c.IDs()), "options; 20 bands each; SHA256", c.SourceSHA256())
}
