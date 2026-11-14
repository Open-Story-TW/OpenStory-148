package wz

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The reader is checked against a real client, which the repository cannot
// carry: set MAPLEGO_CLIENT_WZ_ROOT to a folder holding Item.wz.
func TestCanvasFromClient(t *testing.T) {
	root := os.Getenv("MAPLEGO_CLIENT_WZ_ROOT")
	if root == "" {
		t.Skip("set MAPLEGO_CLIENT_WZ_ROOT to a game client folder")
	}
	archive, err := Open(filepath.Join(root, "Item.wz"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	img, err := archive.Canvas("Consume/0200.img/02000000/info/icon")
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() < 8 || b.Dx() > 64 || b.Dy() < 8 || b.Dy() > 64 {
		t.Fatalf("icon bounds %v", b)
	}
	if _, err = archive.Canvas("Consume/0200.img/02999999/info/icon"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing icon: %v", err)
	}
}

func TestOpenRejectsOtherFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Item.wz")
	if err := os.WriteFile(path, []byte("not a wz archive at all, only some text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if archive, err := Open(path); err == nil {
		archive.Close()
		t.Fatal("opened a file that is not an archive")
	}
}
