package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestItemTexts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "String.wz"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Eqp":     `<imgdir name="Eqp.img"><imgdir name="Eqp"><imgdir name="Weapon"><imgdir name="1302000"><string name="name" value="劍"/></imgdir></imgdir><imgdir name="Face"><imgdir name="20000"><string name="name" value="臉"/></imgdir></imgdir></imgdir></imgdir>`,
		"Consume": `<imgdir name="Consume.img"><imgdir name="2000000"><string name="name" value="紅色藥水"/><string name="desc" value="恢復HP50"/></imgdir><imgdir name="2000001"><string name="name" value="橘色藥水"/></imgdir></imgdir>`,
		"Ins":     `<imgdir name="Ins.img"/>`, "Etc": `<imgdir name="Etc.img"/>`, "Cash": `<imgdir name="Cash.img"/>`, "Pet": `<imgdir name="Pet.img"/>`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, "String.wz", name+".img.xml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if text, ok := ItemName(root, 2000000); !ok || text.Name != "紅色藥水" || text.Desc != "恢復HP50" {
		t.Fatalf("potion text = %+v, %v", text, ok)
	}
	if _, ok := ItemName(root, 20000); ok {
		t.Fatal("a face is not an item")
	}
	byName, err := SearchItems(root, "藥水", 10)
	if err != nil || len(byName) != 2 || byName[0].ID != 2000000 {
		t.Fatalf("name search = %+v, %v", byName, err)
	}
	if byID, _ := SearchItems(root, "1302", 10); len(byID) != 1 || byID[0].Name != "劍" {
		t.Fatalf("id search = %+v", byID)
	}
	if limited, _ := SearchItems(root, "藥水", 1); len(limited) != 1 {
		t.Fatalf("limit ignored: %+v", limited)
	}
	if none, err := SearchItems("", "劍", 10); err != nil || len(none) != 0 {
		t.Fatalf("no root = %+v, %v", none, err)
	}
}

func TestItemIconPath(t *testing.T) {
	for id, want := range map[int]string{
		1302000: "Character.wz:Weapon/01302000.img/info/icon",
		1002357: "Character.wz:Cap/01002357.img/info/icon",
		2000000: "Item.wz:Consume/0200.img/02000000/info/icon",
		3010000: "Item.wz:Install/0301.img/03010000/info/icon",
		4000001: "Item.wz:Etc/0400.img/04000001/info/icon",
		5010000: "Item.wz:Cash/0501.img/05010000/info/icon",
		5000000: "Item.wz:Pet/5000000.img/info/icon",
	} {
		archive, path, ok := ItemIconPath(id)
		if !ok || archive+":"+path != want {
			t.Errorf("%d: %s:%s %v, want %s", id, archive, path, ok, want)
		}
	}
	for _, id := range []int{0, 30000, 1990000, 6000000} {
		if _, _, ok := ItemIconPath(id); ok {
			t.Errorf("%d has an icon path", id)
		}
	}
}
