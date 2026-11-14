package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestItemDefinitionsRejectUnknownAndReadWZ(t *testing.T) {
	item, err := ItemDefinition("", 1041011)
	if err != nil || !item.AllowsSlot(-5) || item.Gender != 1 {
		t.Fatalf("female starter=%+v %v", item, err)
	}
	if _, err = ItemDefinition("", 1999999); err == nil {
		t.Fatal("invented item accepted")
	}
	root := t.TempDir()
	path := filepath.Join(root, "Character.wz", "Weapon")
	if err = os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir name="01322005.img"><imgdir name="info"><int name="reqLevel" value="10"/><int name="reqSTR" value="20"/><int name="reqJob" value="1"/><int name="equipTradeBlock" value="1"/></imgdir></imgdir>`
	if err = os.WriteFile(filepath.Join(path, "01322005.img.xml"), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	item, err = ItemDefinition(root, 1322005)
	if err != nil || !item.AllowsSlot(-11) || item.ReqLevel != 10 || item.ReqSTR != 20 || item.ReqJob != 1 || item.Droppable() {
		t.Fatalf("WZ item=%+v %v", item, err)
	}
	path = filepath.Join(root, "Item.wz", "Consume")
	if err = os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	xml = `<imgdir name="0200.img"><imgdir name="02000000"><imgdir name="info"><int name="slotMax" value="300"/></imgdir></imgdir></imgdir>`
	if err = os.WriteFile(filepath.Join(path, "0200.img.xml"), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	item, err = ItemDefinition(root, 2000000)
	if err != nil || item.SlotMax != 300 || item.InvType != 2 || !item.Droppable() {
		t.Fatalf("WZ potion=%+v %v", item, err)
	}
}

func TestOmittedWZSlotMaxUsesBundleDefaultAcrossInventories(t *testing.T) {
	// Quest1034 grants4031792 ten at once. Its real WZ info has price1 and
	// quest1, but no slotMax. The old default1 split it into ten slots.
	cases := []struct {
		id                    int
		category, group, info string
		want                  int
	}{
		{2000000, "Consume", "0200", `<int name="price" value="3"/>`, 100},
		{3000000, "Install", "0300", `<int name="price" value="1"/>`, 100},
		{4031792, "Etc", "0403", `<int name="price" value="1"/><int name="quest" value="1"/>`, 100},
		{5010000, "Cash", "0501", `<int name="cash" value="1"/>`, 100},
		{2070000, "Consume", "0207", `<int name="price" value="1"/>`, 100},
		{2330000, "Consume", "0233", `<int name="slotMax" value="800"/>`, 800},
		{2070001, "Consume", "0207", `<int name="slotMax" value="1000"/>`, 1000},
		{4031793, "Etc", "0403", `<int name="quest" value="1"/><int name="slotMax" value="1"/>`, 1},
		{2000001, "Consume", "0200", `<int name="slotMax" value="300"/>`, 300},
		{5010001, "Cash", "0501", `<int name="cash" value="1"/><int name="slotMax" value="1"/>`, 1},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.id), func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "Item.wz", tc.category)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			xml := fmt.Sprintf(`<imgdir name="%s.img"><imgdir name="%08d"><imgdir name="info">%s</imgdir></imgdir></imgdir>`, tc.group, tc.id, tc.info)
			if err := os.WriteFile(filepath.Join(dir, tc.group+".img.xml"), []byte(xml), 0644); err != nil {
				t.Fatal(err)
			}
			item, err := ItemDefinition(root, tc.id)
			if err != nil || item.SlotMax != tc.want || item.InvType != tc.id/1000000 {
				t.Fatalf("item%v err%v want%d", item, err, tc.want)
			}
			if tc.id == 4031792 && (!item.Quest || item.Droppable() || item.Price != 1) {
				t.Fatalf("stack default changed quest restrictions: %+v", item)
			}
		})
	}
	root := t.TempDir()
	dir := filepath.Join(root, "Character.wz", "Weapon")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "01322005.img.xml"), []byte(`<imgdir name="01322005.img"><imgdir name="info"><int name="incPAD" value="19"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	item, err := ItemDefinition(root, 1322005)
	if err != nil || item.SlotMax != 1 {
		t.Fatalf("equipment default %+v %v", item, err)
	}
}
