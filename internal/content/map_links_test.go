package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkedMapKeepsOriginalFieldRules(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Map.wz", "Map", "Map2")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{
		"222020110": `<imgdir name="222020110.img"><imgdir name="info"><int name="returnMap" value="100000000"/></imgdir><imgdir name="portal"><imgdir name="0"><int name="pt" value="0"/><string name="pn" value="sp"/><int name="x" value="-72"/><int name="y" value="96"/></imgdir></imgdir></imgdir>`,
		"222020111": `<imgdir name="222020111.img"><imgdir name="info"><int name="returnMap" value="222020200"/><int name="link" value="222020110"/></imgdir></imgdir>`,
		"222020112": `<imgdir name="222020112.img"><imgdir name="info"><int name="link" value="222020112"/></imgdir></imgdir>`,
	}
	for id, data := range fields {
		if err := os.WriteFile(filepath.Join(dir, id+".img.xml"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := catalog.Map(222020111)
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := linked.Portal("sp"); !ok || entry.X != -72 || entry.Y != 96 || linked.ID != 222020111 || linked.ReturnMap != 222020200 {
		t.Fatalf("linked map %+v, entry %+v", linked, entry)
	}
	if _, err := catalog.Map(222020112); err == nil {
		t.Fatal("self-referential map link must fail")
	}
}
