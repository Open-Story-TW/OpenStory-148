package content

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestResolvePotentialEffectsRecognizesAllAttackProcLines(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Item.wz"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Character.wz", "Weapon"), 0755); err != nil {
		t.Fatal(err)
	}
	weaponXML := `<imgdir name="01302000.img"><imgdir name="info"><int name="reqLevel" value="10"/></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Character.wz", "Weapon", "01302000.img.xml"), []byte(weaponXML), 0644); err != nil {
		t.Fatal(err)
	}
	optXML := `<imgdir name="ItemOption.img">
		<imgdir name="010221"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1000"/><int name="level" value="1"/><int name="prop" value="10"/></imgdir></imgdir></imgdir>
		<imgdir name="010226"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1001"/><int name="level" value="1"/><int name="prop" value="5"/></imgdir></imgdir></imgdir>
		<imgdir name="010231"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1002"/><int name="level" value="1"/><int name="prop" value="10"/></imgdir></imgdir></imgdir>
		<imgdir name="010236"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1003"/><int name="level" value="2"/><int name="prop" value="10"/></imgdir></imgdir></imgdir>
		<imgdir name="010241"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1004"/><int name="level" value="2"/><int name="prop" value="5"/></imgdir></imgdir></imgdir>
		<imgdir name="010246"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1005"/><int name="level" value="1"/><int name="prop" value="5"/></imgdir></imgdir></imgdir>
		<imgdir name="019999"><imgdir name="info"><int name="optionType" value="10"/><int name="weight" value="1"/></imgdir><imgdir name="level"><imgdir name="1"><int name="attackType" value="1999"/><int name="level" value="1"/><int name="prop" value="5"/></imgdir></imgdir></imgdir>
	</imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Item.wz", "ItemOption.img.xml"), []byte(optXML), 0644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		optID   uint16
		wantSID int
		wantLv  int
		wantPr  int
	}{
		{10221, 90001003, 1, 10},
		{10226, 90001001, 1, 5},
		{10231, 90001002, 1, 10},
		{10236, 90001004, 2, 10},
		{10241, 90001006, 2, 5},
		{10246, 90001005, 1, 5},
	}
	for _, tc := range cases {
		c := domain.Character{Equipment: []domain.Equipment{{
			ID: 1, Slot: -11, ItemID: 1302000,
			Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{tc.optID}},
		}}}
		got, err := ResolvePotentialEffects(root, c, now)
		if err != nil {
			t.Fatalf("opt %d: %v", tc.optID, err)
		}
		if len(got.Pending) != 0 {
			t.Fatalf("opt %d unexpectedly pending: %+v", tc.optID, got.Pending)
		}
		if len(got.AttackProcs) != 1 {
			t.Fatalf("opt %d: got %d AttackProcs, want 1", tc.optID, len(got.AttackProcs))
		}
		p := got.AttackProcs[0]
		if p.SkillID != tc.wantSID || p.Level != tc.wantLv || p.Prop != tc.wantPr {
			t.Fatalf("opt %d: got %+v, want skill=%d lv=%d prop=%d", tc.optID, p, tc.wantSID, tc.wantLv, tc.wantPr)
		}
	}

	// Unknown attackType must remain Pending rather than being silently dropped.
	bad := domain.Character{Equipment: []domain.Equipment{{
		ID: 1, Slot: -11, ItemID: 1302000,
		Potential: domain.EquipmentPotential{Present: true, RawState: 17, Options: [5]uint16{19999}},
	}}}
	got, err := ResolvePotentialEffects(root, bad, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AttackProcs) != 0 || len(got.Pending) != 1 {
		t.Fatalf("unknown attackType: AttackProcs=%d Pending=%d", len(got.AttackProcs), len(got.Pending))
	}
}
