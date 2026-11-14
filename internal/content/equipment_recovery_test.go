package content

import (
	"fmt"
	"maple-go-server/internal/domain"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func topRecoveryFixture(t *testing.T, root string, id int, properties string) {
	t.Helper()
	category, _ := equipmentCategory(id / 10000)
	dir := filepath.Join(root, "Character.wz", category)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir name="top"><imgdir name="info">` + properties + `</imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%08d.img.xml", id)), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
}
func TestOrdinaryTopRecoveryOriginalWZ(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("original WZ required")
	}
	for _, id := range []int{1050018, 1050100, 1050127, 1051017, 1051098, 1051140} {
		def, err := loadTopRecoveryDefinition(root, id)
		if err != nil || def.cash || def.multiplier != 1.5 {
			t.Fatal(id, def, err)
		}
		c := domain.Character{MapID: 105000011, HP: 1, Equipment: []domain.Equipment{{ItemID: id, Slot: -5}}}
		p, err := RequestedRecoveryPolicy(root, c, time.Now(), false, 0)
		if err != nil || p.HP != 30 || p.MP != 9 {
			t.Fatal("original rate2*top1.5", id, p, err)
		}
		c.MapID = 105000012
		p, err = RequestedRecoveryPolicy(root, c, time.Now(), false, 0)
		if err != nil || p.HP != 45 || p.MP != 13 {
			t.Fatal("double product before final truncation", id, p, err)
		}
	}
	def, err := loadTopRecoveryDefinition(root, 1040002)
	if err != nil || def.cash || def.multiplier != 1 {
		t.Fatal("original omitted recovery", def, err)
	}
}
func TestOrdinaryTopRecoveryStrictSourceAndNoStack(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	topRecoveryFixture(t, root, 1040002, `<float name="recovery" value="1.5"/>`)
	topRecoveryFixture(t, root, 1050018, `<float name="recovery" value="2"/>`)
	topRecoveryFixture(t, root, 1040003, `<float name="recovery" value="NaN"/><int name="cash" value="1"/>`)
	base := domain.Equipment{ID: 1, ItemID: 1040002, Slot: -5}
	for _, tc := range []struct {
		name string
		e    domain.Equipment
		want float64
	}{
		{"active", base, 3},
		{"bag", domain.Equipment{ItemID: 1040002, Slot: 1}, 2},
		{"other worn piece", domain.Equipment{ItemID: 1040002, Slot: -6}, 2},
		{"cash overlay", domain.Equipment{ItemID: 1040002, Slot: -105}, 2},
		{"cash serial", domain.Equipment{ItemID: 1040002, Slot: -5, CashID: 99}, 2},
		{"cash template", domain.Equipment{ItemID: 1040003, Slot: -5}, 2},
		{"expired", domain.Equipment{ItemID: 1040002, Slot: -5, ExpiresAt: now.UnixMilli()}, 2},
		{"expires later", domain.Equipment{ItemID: 1040002, Slot: -5, ExpiresAt: now.Add(time.Second).UnixMilli()}, 3},
	} {
		got, err := ordinaryRecoveryRate(root, domain.Character{Equipment: []domain.Equipment{tc.e}}, now, 2)
		if err != nil || got != tc.want {
			t.Fatal(tc.name, got, err)
		}
	}
	c := domain.Character{Equipment: []domain.Equipment{base, {ItemID: 1050018, Slot: -5}}}
	if _, err := ordinaryRecoveryRate(root, c, now, 2); err == nil {
		t.Fatal("multiple physical tops multiplied")
	}
	c.Equipment[1].Slot = -105
	if got, err := ordinaryRecoveryRate(root, c, now, 2); err != nil || got != 3 {
		t.Fatal("cosmetic stacked", got, err)
	}
	// Gate by original map rate, not by multiplied output or a universal gear boost.
	for _, rate := range []float64{0, 0.5, 1} {
		c.Equipment = []domain.Equipment{{ItemID: 9999999, Slot: -5}}
		if got, err := ordinaryRecoveryRate(root, c, now, rate); err != nil || got != rate {
			t.Fatal("map<=1 consulted gear", got, err)
		}
	}
}
func TestOrdinaryTopRecoveryCacheAndInvalidFacts(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	topRecoveryFixture(t, a, 1040002, `<float name="recovery" value="1.5"/>`)
	topRecoveryFixture(t, b, 1040002, `<float name="recovery" value="2"/>`)
	c := domain.Character{Equipment: []domain.Equipment{{ItemID: 1040002, Slot: -5}}}
	if rate, err := ordinaryRecoveryRate(a, c, time.Now(), 2); err != nil || rate != 3 {
		t.Fatal(rate, err)
	}
	topRecoveryFixture(t, a, 1040002, `<float name="recovery" value="9"/>`)
	if rate, err := ordinaryRecoveryRate(a, c, time.Now(), 2); err != nil || rate != 3 {
		t.Fatal("cache reread", rate, err)
	}
	if rate, err := ordinaryRecoveryRate(b, c, time.Now(), 2); err != nil || rate != 4 {
		t.Fatal("root alias", rate, err)
	}
	d, err := loadTopRecoveryDefinition(a, 1040002)
	if err != nil {
		t.Fatal(err)
	}
	d.multiplier = 99
	if rate, err := ordinaryRecoveryRate(a, c, time.Now(), 2); err != nil || rate != 3 {
		t.Fatal("mutable cache return", rate, err)
	}
	for i, value := range []string{"NaN", "+Inf", "-1", "bad", "1e308"} {
		id := 1040010 + i
		topRecoveryFixture(t, b, id, `<float name="recovery" value="`+value+`"/>`)
		c.Equipment[0].ItemID = id
		if _, err := ordinaryRecoveryRate(b, c, time.Now(), 2); err == nil {
			t.Fatal("bad amount admitted", value)
		}
	}
	for _, properties := range []string{`<float name="recovery" value="1"/><float name="recovery" value="2"/>`, `<int name="cash" value="bad"/>`} {
		root := t.TempDir()
		topRecoveryFixture(t, root, 1040002, properties)
		c.Equipment[0].ItemID = 1040002
		if _, err := ordinaryRecoveryRate(root, c, time.Now(), 2); err == nil {
			t.Fatal("ambiguous metadata admitted")
		}
	}
	c.Equipment[0].ItemID = 1040999
	if _, err := ordinaryRecoveryRate(b, c, time.Now(), 2); err == nil {
		t.Fatal("missing top became invented default")
	}
}
func TestOrdinaryTopRecoveryDoubleOrderAndThreeSources(t *testing.T) {
	root := t.TempDir()
	topRecoveryFixture(t, root, 1040002, `<float name="recovery" value="1.5"/>`)
	for i, tc := range []struct {
		mapRate string
		hp, mp  int
	}{{"3", 45, 13}, {"1.01", 15, 4}, {"1", 10, 3}, {"0", 0, 0}} {
		recoveryMapFixture(t, root, i+1, tc.mapRate, 0)
		c := domain.Character{MapID: i + 1, HP: 1, Equipment: []domain.Equipment{{ItemID: 1040002, Slot: -5}}}
		p, err := RequestedRecoveryPolicy(root, c, time.Now(), false, 0)
		if err != nil || p.HP != tc.hp || p.MP != tc.mp {
			t.Fatal(tc, p, err)
		}
		hp, mp, err := ScaleReviewedRecovery(root, c.MapID, 20, 17)
		wantHP, wantMP := []int{60, 20, 20, 20}[i], []int{51, 17, 17, 17}[i]
		if err != nil || hp != wantHP || mp != wantMP {
			t.Fatal("top multiplier leaked to4s", hp, mp, err)
		}
	}
}

func TestOrdinaryTopRecoveryNativeSignedAmountBoundary(t *testing.T) {
	root := t.TempDir()
	recoveryMapFixture(t, root, 1, "2", 0)
	for _, tc := range []struct {
		id      int
		value   string
		hp, mp  int
		invalid bool
	}{{1040002, "0", 0, 0, false}, {1040003, "0.25", 5, 1, false}, {1040004, "1638.35", 32767, 9830, false}, {1040005, "1638.4", 0, 0, true}} {
		topRecoveryFixture(t, root, tc.id, `<float name="recovery" value="`+tc.value+`"/>`)
		c := domain.Character{HP: 1, MapID: 1, Equipment: []domain.Equipment{{Slot: -5, ItemID: tc.id}}}
		p, err := RequestedRecoveryPolicy(root, c, time.Now(), false, 0)
		if tc.invalid {
			if err == nil {
				t.Fatal("signed native amount overflow accepted", p)
			}
		} else if err != nil || p.HP != tc.hp || p.MP != tc.mp {
			t.Fatal(tc, p, err)
		}
	}
}
