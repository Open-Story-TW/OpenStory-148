package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePotentialItemFixture(t *testing.T, options string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "Character.wz", "Glove")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := `<imgdir name="01082392.img"><imgdir name="info"><int name="reqLevel" value="120"/>` + options + `</imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(dir, "01082392.img.xml"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestItemPotentialTemplateFactsPreserveAllPositionsAndDoNotGenerateInstance(t *testing.T) {
	root := writePotentialItemFixture(t, `<imgdir name="option">
<imgdir name="0"><int name="option" value="60003"/><int name="level" value="1"/></imgdir>
<imgdir name="1"><int name="option" value="10001"/><int name="level" value="0"/></imgdir>
<imgdir name="2"><int name="option" value="10002"/><int name="level" value="-1"/></imgdir>
<imgdir name="3"><int name="option" value="0"/><int name="level" value="21"/></imgdir>
<imgdir name="4"><int name="option" value="65535"/><int name="level" value="22"/></imgdir>
</imgdir>`)
	item, err := ItemDefinition(root, 1082392)
	if err != nil || item.FixedPotentialOptionCount != 5 || item.FixedPotentialOptions[0] != (ItemPotentialOption{60003, 1}) || item.FixedPotentialOptions[4] != (ItemPotentialOption{65535, 22}) || item.FixedPotentialOptions[2].Level != -1 {
		t.Fatal("template facts were dropped or interpreted", item, err)
	}
	if !item.Equipment.Potential.IsZero() {
		t.Fatal("fact loading silently generated an instance potential")
	}
	item.FixedPotentialOptions[0].ID = 123
	again, err := ItemDefinition(root, 1082392)
	if err != nil || again.FixedPotentialOptions[0].ID != 60003 {
		t.Fatal("caller mutated cached template facts", again, err)
	}
}

func TestItemPotentialTemplateMalformedFactsAreNotSilentlyDefaulted(t *testing.T) {
	row := `<imgdir name="0"><int name="option" value="60003"/><int name="level" value="1"/></imgdir>`
	for name, body := range map[string]string{
		"missing position":   strings.Replace(row, `name="0"`, `name="1"`, 1),
		"duplicate position": row + row,
		"invalid level":      strings.Replace(row, `value="1"`, `value="bad"`, 1),
		"overflow ID":        strings.Replace(row, `value="60003"`, `value="65536"`, 1),
		"wrong type":         strings.Replace(row, `<int name="level"`, `<string name="level"`, 1),
		"six positions":      row + strings.ReplaceAll(row, `name="0"`, `name="1"`) + strings.ReplaceAll(row, `name="0"`, `name="2"`) + strings.ReplaceAll(row, `name="0"`, `name="3"`) + strings.ReplaceAll(row, `name="0"`, `name="4"`) + strings.ReplaceAll(row, `name="0"`, `name="5"`),
	} {
		t.Run(name, func(t *testing.T) {
			root := writePotentialItemFixture(t, `<imgdir name="option">`+body+`</imgdir>`)
			if _, err := ItemDefinition(root, 1082392); err == nil {
				t.Fatal("ambiguous source became apparently valid metadata")
			}
		})
	}
}

func TestItemPotentialMissingLeafFieldsUseProvenNativeZeroDefaults(t *testing.T) {
	root := writePotentialItemFixture(t, `<imgdir name="option"><imgdir name="0"><int name="option" value="60003"/></imgdir><imgdir name="1"><int name="level" value="2"/></imgdir><imgdir name="2"/></imgdir>`)
	item, err := ItemDefinition(root, 1082392)
	if err != nil || item.FixedPotentialOptionCount != 3 || item.FixedPotentialOptions[0] != (ItemPotentialOption{60003, 0}) || item.FixedPotentialOptions[1] != (ItemPotentialOption{0, 2}) || item.FixedPotentialOptions[2] != (ItemPotentialOption{}) {
		t.Fatal("native missing-leaf defaults changed", item, err)
	}
}

func TestItemPotentialActualFixedGloveOverrides(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("set MAPLEGO_WZ_ROOT for original fixed-option glove sources")
	}
	for i := 0; i < 3; i++ {
		item, err := ItemDefinition(root, 1082392+i)
		if err != nil || item.ReqLevel != 120 || item.FixedPotentialOptionCount != 3 {
			t.Fatal("original fixed option override changed", item, err)
		}
		for line := 0; line < 3; line++ {
			if item.FixedPotentialOptions[line] != (ItemPotentialOption{60001 + line, 1 + i}) {
				t.Fatal("original per-line fixed option override changed", item.FixedPotentialOptions)
			}
		}
	}
}
