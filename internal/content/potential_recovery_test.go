package content

import (
	"encoding/json"
	"fmt"
	"maple-go-server/internal/domain"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func recoveryMapFixture(t *testing.T, root string, id int, rate string, kind int) {
	t.Helper()
	p := filepath.Join(root, "Map.wz", "Map", fmt.Sprintf("Map%d", id/100000000))
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, fmt.Sprintf("%09d.img.xml", id)), []byte(fmt.Sprintf(`<imgdir name="map"><imgdir name="info"><float name="recovery" value="%s"/><int name="fieldType" value="%d"/></imgdir></imgdir>`, rate, kind)), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestRequestedRecoveryExactNativeSourcesAndRate(t *testing.T) {
	now := time.Now()
	root := effectsFixture(t, 5)
	c := domain.Character{MapID: 1, Job: 200, HP: 50, Equipment: []domain.Equipment{revealedEffectsGear(10001)}}
	// Add separate original periodic options; do not use HP/MP attack-proc keys.
	path := filepath.Join(root, "Item.wz/ItemOption.img.xml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	extra := `<imgdir name="010151"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="RecoveryHP" value="2"/></imgdir></imgdir></imgdir><imgdir name="010156"><imgdir name="info"><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="RecoveryMP" value="3"/></imgdir></imgdir></imgdir>`
	data = append(data[:len(data)-len("</imgdir>")], []byte(extra+"</imgdir>")...)
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	c.Equipment[0].Potential.Options = [5]uint16{10151, 10151, 10156, 10156, 10151}
	for i, rate := range []string{"1", "1.5", "0.25", "0", "2.2"} {
		c.MapID = i + 1
		recoveryMapFixture(t, root, c.MapID, rate, 0)
		got, err := RequestedRecoveryPolicy(root, c, now, true, 0)
		if err != nil || got.HP != 6 || got.MP != 6 || got.Interval != 4*time.Second {
			t.Fatal("potential was rate-scaled or omitted line", got, err)
		}
		p, err := RequestedRecoveryPolicy(root, c, now, false, 0)
		wantHP := []int{10, 15, 2, 0, 22}[i]
		wantMP := []int{3, 4, 0, 0, 6}[i]
		if err != nil || p.HP != wantHP || p.MP != wantMP || p.Interval != 10*time.Second {
			t.Fatal("native rate one truncation", p, err)
		}
		if rate == "0" {
			hp, mp, err := ScaleReviewedRecovery(root, c.MapID, 20, 17)
			if err != nil || hp != 20 || mp != 17 || p.HP != 0 || p.MP != 0 || got.HP != 6 || got.MP != 6 {
				t.Fatal("zero map rate mixed passive/native91/flat92 policies", hp, mp, p, got, err)
			}
		}
	}
	c.Job = 3112
	if p, err := RequestedRecoveryPolicy(root, c, now, true, 0); err != nil || p.MP != 0 || p.HP != 6 {
		t.Fatal("Demon Fury recovered", p, err)
	}
	for _, field := range []int{1, 2} {
		c.MapID = field
		if _, err := RequestedRecoveryPolicy(root, c, now, false, 2); err == nil {
			t.Fatal("unknown chair state admitted")
		}
	}
	c.MapID = 99
	recoveryMapFixture(t, root, 99, "1", 36)
	if _, err := RequestedRecoveryPolicy(root, c, now, true, 0); err == nil {
		t.Fatal("field36 special case guessed")
	}
}
func TestRecoveryMapCacheRootIsolationAndInvalidData(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	recoveryMapFixture(t, a, 1, "1.5", 0)
	recoveryMapFixture(t, b, 1, "2", 0)
	c := domain.Character{MapID: 1, HP: 1}
	p, err := RequestedRecoveryPolicy(a, c, time.Now(), false, 0)
	if err != nil || p.HP != 15 {
		t.Fatal(p, err)
	}
	recoveryMapFixture(t, a, 1, "3", 0)
	p, err = RequestedRecoveryPolicy(a, c, time.Now(), false, 0)
	if err != nil || p.HP != 15 {
		t.Fatal("cache reread", p, err)
	}
	p, err = RequestedRecoveryPolicy(b, c, time.Now(), false, 0)
	if err != nil || p.HP != 20 {
		t.Fatal("roots aliased", p, err)
	}
	for i, rate := range []string{"NaN", "+Inf", "-1", "bad", "10000"} {
		c.MapID = i + 10
		recoveryMapFixture(t, b, c.MapID, rate, 0)
		if _, err = RequestedRecoveryPolicy(b, c, time.Now(), false, 0); err == nil {
			t.Fatal("bad rate became heal", rate)
		}
	}
}
func TestEmbeddedRecoveryMapsMatchOriginalWZ(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("original WZ required")
	}
	var bundle struct {
		Maps []Map `json:"maps"`
	}
	if err := json.Unmarshal(starter, &bundle); err != nil {
		t.Fatal(err)
	}
	for _, m := range bundle.Maps {
		a, err := loadRecoveryMap("", m.ID)
		if err != nil {
			t.Fatal(err)
		}
		b, err := loadRecoveryMap(root, m.ID)
		if err != nil || !reflect.DeepEqual(a, b) {
			t.Fatal("starter source facts changed", m.ID, a, b, err)
		}
	}
}

func TestReviewedRecoveryMapScalingAfterCompleteAmount(t *testing.T) {
	root := t.TempDir()
	for i, tc := range []struct {
		rate   string
		hp, mp int
	}{{"2", 40, 34}, {"3", 60, 51}, {"2.2", 44, 37}, {"0.25", 5, 4}, {"0", 20, 17}} {
		recoveryMapFixture(t, root, i+1, tc.rate, 0)
		hp, mp, err := ScaleReviewedRecovery(root, i+1, 20, 17)
		if err != nil || hp != tc.hp || mp != tc.mp {
			t.Fatal(tc, hp, mp, err)
		}
	}
	recoveryMapFixture(t, root, 99, "1", 36)
	if _, _, err := ScaleReviewedRecovery(root, 99, 20, 17); err == nil {
		t.Fatal("field36 admitted")
	}
	if hp, mp, err := ScaleReviewedRecovery(root, 1, -1, 17); err == nil || hp != 0 || mp != 0 {
		t.Fatal("invalid amount admitted", hp, mp, err)
	}
	if hp, mp, err := ScaleReviewedRecovery(root, 1, 2147483647, 17); err == nil || hp != 0 || mp != 0 {
		t.Fatal("overflow admitted", hp, mp, err)
	}
}
