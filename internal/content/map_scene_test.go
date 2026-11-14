package content

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMountedSceneConstrainsTerminalTransfer(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Effect.wz"), 0700); err != nil {
		t.Fatal(err)
	}
	data := `<imgdir name="Direction3.img"><imgdir name="goLith"><imgdir name="Scene0"><imgdir name="0"><int name="type" value="0"/></imgdir><imgdir name="1"><int name="type" value="2"/><int name="start" value="5000"/><int name="field" value="104000000"/></imgdir></imgdir><imgdir name="Double"><imgdir name="0"><int name="type" value="2"/><int name="start" value="5000"/><int name="field" value="1"/></imgdir><imgdir name="1"><int name="type" value="2"/><int name="start" value="6000"/><int name="field" value="2"/></imgdir></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(root, "Effect.wz", "Direction3.img.xml"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMapScene(root, "Effect/Direction3.img/goLith/Scene0")
	if err != nil || got.Transition.TargetMap != 104000000 || got.Transition.After != 5*time.Second {
		t.Fatalf("scene=%+v err=%v", got, err)
	}
	for _, path := range []string{"Effect/Direction3.img/goLith/Double", "Effect/Direction3.img/goLith/Missing", "Effect/../Direction3.img/goLith/Scene0", "../Effect/Direction3.img/goLith/Scene0"} {
		if _, err := LoadMapScene(root, path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
