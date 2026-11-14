package content

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNativeSourceMesoEligibilityAndRanges(t *testing.T) {
	for _, tc := range []struct {
		name, extra                            string
		level, count, chance, minimum, maximum int
	}{
		{"ordinary", "", 10, 1, 600000, 6, 10},
		{"boss", `<int name="boss" value="1"/>`, 30, 2, 800000, 594, 900},
		{"invincible", `<int name="invincible" value="1"/>`, 10, 0, 0, 0, 0},
		{"periodic_drop_mob", `<int name="dropItemPeriod" value="5"/>`, 10, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "Mob.wz")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			xml := `<imgdir name="0100100.img"><imgdir name="info"><int name="level" value="` + strconv.Itoa(tc.level) + `"/><int name="PDRate" value="10"/><int name="MDRate" value="10"/>` + tc.extra + `</imgdir></imgdir>`
			if err := os.WriteFile(filepath.Join(dir, "0100100.img.xml"), []byte(xml), 0600); err != nil {
				t.Fatal(err)
			}
			rule, err := MonsterMesoRule(root, 100100)
			if err != nil || rule.Count != tc.count || rule.Chance != tc.chance || rule.Minimum != tc.minimum || rule.Maximum != tc.maximum {
				t.Fatalf("source meso rule: %+v %v", rule, err)
			}
			if rule, err := MonsterMesoRule(root, 9300018); err != nil || rule.Count != 0 {
				t.Fatalf("tutorial mob created money: %+v %v", rule, err)
			}
		})
	}
}
