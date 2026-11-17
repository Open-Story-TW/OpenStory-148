package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRomeoLabReactorLoadingIsScopedToItsWZMaps(t *testing.T) {
	root := t.TempDir()
	mapDir := filepath.Join(root, "Map.wz", "Map", "Map9")
	if err := os.MkdirAll(mapDir, 0700); err != nil {
		t.Fatal(err)
	}
	const field = `<imgdir name="field"><imgdir name="reactor">
	<imgdir name="0"><string name="id" value="2618000"/><int name="x" value="45"/><int name="y" value="-54"/><int name="reactorTime" value="-1"/></imgdir>
	<imgdir name="1"><string name="id" value="2618006"/><int name="x" value="1052"/><int name="y" value="163"/><int name="reactorTime" value="-1"/></imgdir>
	</imgdir></imgdir>`
	for _, file := range []string{"926100100.img.xml", "926110100.img.xml", "926100101.img.xml"} {
		if err := os.WriteFile(filepath.Join(mapDir, file), []byte(field), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, mapID := range []int{926100100, 926110100} {
		reactors, err := MapReactors(root, mapID)
		if err != nil || len(reactors) != 2 ||
			reactors[0] != (Reactor{TemplateID: 2618000, X: 45, Y: -54, Respawn: -1}) ||
			reactors[1].TemplateID != 2618006 {
			t.Fatalf("map %d reactors: %+v, %v", mapID, reactors, err)
		}
	}
	outside, err := MapReactors(root, 926100101)
	if err != nil || len(outside) != 0 {
		t.Fatalf("reactors outside the quest: %+v, %v", outside, err)
	}
}
