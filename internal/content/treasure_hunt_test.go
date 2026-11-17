package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreasureReactorsAreLoadedOnlyInEventMaps(t *testing.T) {
	root := t.TempDir()
	mapDir := filepath.Join(root, "Map.wz", "Map", "Map1")
	if err := os.MkdirAll(mapDir, 0700); err != nil {
		t.Fatal(err)
	}
	const field = `<imgdir name="field"><imgdir name="reactor">
	<imgdir name="1"><string name="id" value="9002002"/><int name="x" value="520"/><int name="y" value="34"/><int name="reactorTime" value="270"/></imgdir>
	<imgdir name="2"><string name="id" value="9001000"/><int name="reactorTime" value="-1"/></imgdir>
	</imgdir></imgdir>`
	for _, filename := range []string{"109010100.img.xml", "109050000.img.xml"} {
		if err := os.WriteFile(filepath.Join(mapDir, filename), []byte(field), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reactors, err := MapReactors(root, 109010100)
	if err != nil || len(reactors) != 2 || reactors[0] != (Reactor{Index: 1, TemplateID: 9002002, X: 520, Y: 34, Respawn: 270}) || reactors[1].Respawn != -1 {
		t.Fatalf("treasure reactors: %+v, %v", reactors, err)
	}
	reactors, err = MapReactors(root, 109050000)
	if err != nil || len(reactors) != 0 {
		t.Fatalf("reactors outside the treasure hunt: %+v, %v", reactors, err)
	}
}
