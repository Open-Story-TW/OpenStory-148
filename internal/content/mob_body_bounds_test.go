package content

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func bodyFile(t *testing.T, root string, id int, inner string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "Mob.wz"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", id)), []byte(`<imgdir name="mob.img">`+inner+`</imgdir>`), 0600); err != nil {
		t.Fatal(err)
	}
}
func bodyFrame(name string, l, t, r, b int) string {
	return fmt.Sprintf(`<canvas name="%s"><vector name="origin" x="900" y="900"/><vector name="lt" x="%d" y="%d"/><vector name="rb" x="%d" y="%d"/></canvas>`, name, l, t, r, b)
}
func TestMobBodyBoundsFollowsVisualLinkAndIncludesEveryFrameAndFacing(t *testing.T) {
	root := t.TempDir()
	bodyFile(t, root, 100005, `<imgdir name="info"><int name="maxHP" value="35"/><string name="link" value="0130100"/></imgdir>`)
	bodyFile(t, root, 130100, `<imgdir name="info"><int name="maxHP" value="95"/></imgdir><imgdir name="stand">`+bodyFrame("0", -31, -51, 31, 0)+`</imgdir><imgdir name="move">`+bodyFrame("0", -32, -54, 34, 0)+bodyFrame("1", -34, -54, 31, 0)+`</imgdir>`)
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.MobBodyBounds(100005)
	if err != nil || got.Kind != "static_frame_envelope_both_facings" || got.Envelope != (MobBodyRect{-34, -54, 34, 0}) || got.FrameCount != 3 || len(got.Sources) != 2 || got.Sources[0].ID != 100005 || got.Sources[1].ID != 130100 || len(got.Sources[0].SHA256) != 64 {
		t.Fatal(got, err)
	}
	if got.Envelope == (MobBodyRect{-31, -51, 31, 0}) {
		t.Fatal("only stand0 loaded")
	}
	bodyFile(t, root, 10, `<imgdir name="stand">`+bodyFrame("0", -21, -33, 22, 0)+`</imgdir>`)
	got, err = MobBodyDefinition(root, 10)
	if err != nil || got.UnmirroredEnvelope != (MobBodyRect{-21, -33, 22, 0}) || got.Envelope != (MobBodyRect{-22, -33, 22, 0}) {
		t.Fatal(got, err)
	}
}
func TestMobBodyBoundsUnknownNeverBecomesGuessedRadius(t *testing.T) {
	for _, name := range []string{"missing", "cycle", "depth", "action-uol", "frame-uol", "vector-uol", "malformed", "partial", "empty", "extent", "frames"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			frame := bodyFrame("0", -10, -20, 10, 0)
			inner := `<imgdir name="stand">` + frame + `</imgdir>`
			switch name {
			case "missing":
				root = ""
			case "cycle":
				bodyFile(t, root, 2, `<imgdir name="info"><string name="link" value="1"/></imgdir>`)
				inner += `<imgdir name="info"><string name="link" value="2"/></imgdir>`
			case "depth":
				for i := 2; i <= 9; i++ {
					bodyFile(t, root, i, fmt.Sprintf(`<imgdir name="info"><string name="link" value="%d"/></imgdir>`, i+1))
				}
				inner += `<imgdir name="info"><string name="link" value="2"/></imgdir>`
			case "action-uol":
				inner += `<uol name="move" value="move"/>`
			case "frame-uol":
				inner = `<imgdir name="stand">` + frame + `<uol name="1" value="1"/></imgdir>`
			case "vector-uol":
				inner = `<imgdir name="stand"><canvas name="0"><uol name="lt" value="elsewhere"/><vector name="rb" x="1" y="0"/></canvas></imgdir>`
			case "malformed":
				inner = `<imgdir name="stand"><canvas name="0"><vector name="lt" x="bad" y="-20"/><vector name="rb" x="10" y="0"/></canvas></imgdir>`
			case "partial":
				inner = `<imgdir name="stand"><canvas name="0"><vector name="lt" x="-10" y="-20"/></canvas></imgdir>`
			case "empty":
				inner = `<imgdir name="stand">` + bodyFrame("0", 0, 0, 0, 0) + `</imgdir>`
			case "extent":
				inner = `<imgdir name="stand">` + bodyFrame("0", -4097, -20, 10, 0) + `</imgdir>`
			case "frames":
				var b strings.Builder
				b.WriteString(`<imgdir name="stand">`)
				for i := 0; i < 4097; i++ {
					b.WriteString(bodyFrame(fmt.Sprint(i), -10, -20, 10, 0))
				}
				b.WriteString(`</imgdir>`)
				inner = b.String()
			}
			if root != "" {
				bodyFile(t, root, 1, inner)
			}
			got, err := MobBodyDefinition(root, 1)
			if !errors.Is(err, ErrMobBodyUnknown) || got.Kind != "" || got.Envelope.Nonempty() {
				t.Fatal("unknown manufactured geometry", got, err)
			}
		})
	}
}
func TestMobBodyMissingVectorIsNativeEmptyNotCanvasOrigin(t *testing.T) {
	root := t.TempDir()
	bodyFile(t, root, 1, `<imgdir name="stand"><canvas name="0"><vector name="origin" x="1000" y="1000"/></canvas>`+bodyFrame("1", -4, -5, 6, 0)+`</imgdir>`)
	b, e := MobBodyDefinition(root, 1)
	if e != nil || b.FrameCount != 1 || b.Envelope != (MobBodyRect{-6, -5, 6, 0}) {
		t.Fatal(b, e)
	}
}

func TestMobBodyBoundsMountedBaseline(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("mount pinned WZ root to check the reviewed native targets")
	}
	for _, q := range []struct {
		id, frames int
		rect       MobBodyRect
	}{{100000, 7, MobBodyRect{-22, -33, 22, 0}}, {100005, 6, MobBodyRect{-34, -54, 34, 0}}, {100007, 8, MobBodyRect{-35, -54, 35, 0}}} {
		b, e := MobBodyDefinition(root, q.id)
		if e != nil || b.FrameCount != q.frames || b.Envelope != q.rect || len(b.Sources) != 2 {
			t.Fatal(q, b, e)
		}
	}
}

func TestMobBodyCatalogPositiveCachePinsFilesAndReturnsOwnedSources(t *testing.T) {
	root := t.TempDir()
	bodyFile(t, root, 1, `<imgdir name="stand">`+bodyFrame("0", -3, -5, 4, 0)+`</imgdir>`)
	c, e := NewCatalog(root)
	if e != nil {
		t.Fatal(e)
	}
	first, e := c.MobBodyBounds(1)
	if e != nil {
		t.Fatal(e)
	}
	pinnedHash := first.Sources[0].SHA256
	first.Sources[0].ID = 999
	first.Sources[0].SHA256 = "mutated"
	bodyFile(t, root, 1, `<imgdir name="stand">`+bodyFrame("0", -30, -50, 40, 0)+`</imgdir>`)
	again, e := c.MobBodyBounds(1)
	if e != nil || again.Envelope != (MobBodyRect{-4, -5, 4, 0}) || again.Sources[0].ID != 1 || again.Sources[0].SHA256 != pinnedHash {
		t.Fatal(again, e)
	}
	fresh, e := MobBodyDefinition(root, 1)
	if e != nil || fresh.Envelope == again.Envelope || fresh.Sources[0].SHA256 == pinnedHash {
		t.Fatal("file did not actually change", fresh, e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := c.MobBodyBounds(1)
			if e == nil {
				v.Sources[0].SHA256 = "caller-owned"
			}
		}()
	}
	wg.Wait()
	again, e = c.MobBodyBounds(1)
	if e != nil || again.Sources[0].SHA256 != pinnedHash {
		t.Fatal("caller changed positive cache", again, e)
	}
}

func TestMobBodyCatalogCachesAreRootLocalAndUnknownIsNotPinned(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	bodyFile(t, left, 1, `<imgdir name="stand">`+bodyFrame("0", -3, -5, 4, 0)+`</imgdir>`)
	bodyFile(t, right, 1, `<imgdir name="stand">`+bodyFrame("0", -30, -50, 40, 0)+`</imgdir>`)
	a, _ := NewCatalog(left)
	b, _ := NewCatalog(right)
	av, e := a.MobBodyBounds(1)
	bv, e2 := b.MobBodyBounds(1)
	if e != nil || e2 != nil || av.Envelope == bv.Envelope || av.Sources[0].SHA256 == bv.Sources[0].SHA256 {
		t.Fatal(av, bv, e, e2)
	}
	if _, e = a.MobBodyBounds(2); !errors.Is(e, ErrMobBodyUnknown) {
		t.Fatal(e)
	}
	bodyFile(t, left, 2, `<imgdir name="stand">`+bodyFrame("0", -1, -2, 1, 0)+`</imgdir>`)
	if v, e := a.MobBodyBounds(2); e != nil || !v.Envelope.Nonempty() {
		t.Fatal("negative result was pinned", v, e)
	}
}
