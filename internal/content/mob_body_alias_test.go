package content

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMobBodyAliasesUseTheirOriginalParentAndPreserveFrameGeometry(t *testing.T) {
	root := t.TempDir()
	bodyFile(t, root, 9300018, `<imgdir name="move">`+bodyFrame("0", -10, -36, 11, -14)+
		`<uol name="1" value="0"/></imgdir><imgdir name="stand"><uol name="0" value="../move/1"/></imgdir><uol name="fly" value="move"/>`)
	got, err := MobBodyDefinition(root, 9300018)
	if err != nil || got.FrameCount != 5 || got.UnmirroredEnvelope != (MobBodyRect{-10, -36, 11, -14}) || got.Envelope != (MobBodyRect{-11, -36, 11, -14}) {
		t.Fatalf("local aliases changed geometry: %+v, %v", got, err)
	}
	bodyFile(t, root, 11, `<imgdir name="vectors"><vector name="lt" x="-9" y="-20"/><vector name="rb" x="12" y="0"/></imgdir><imgdir name="stand"><canvas name="0"><uol name="lt" value="../../vectors/lt"/><uol name="rb" value="../../vectors/rb"/></canvas></imgdir>`)
	got, err = MobBodyDefinition(root, 11)
	if err != nil || got.UnmirroredEnvelope != (MobBodyRect{-9, -20, 12, 0}) {
		t.Fatalf("vector aliases changed geometry: %+v, %v", got, err)
	}
}

func TestMobBodyAliasAndXMLWorkBudgets(t *testing.T) {
	var tree bodyWZNode
	if err := xml.Unmarshal([]byte(`<imgdir name="root"><imgdir name="move">`+bodyFrame("0", -10, -20, 10, 0)+`<uol name="1" value="../move/../move/0"/></imgdir></imgdir>`), &tree); err != nil {
		t.Fatal(err)
	}
	r, err := newBodyWZResolver(&tree)
	if err != nil {
		t.Fatal(err)
	}
	r.remaining = 3
	if _, err := r.resolve(tree.child("move").child("1")); !errors.Is(err, ErrMobBodyUnknown) {
		t.Fatal("path operations exceeded the shared budget", err)
	}
	for _, raw := range []string{
		strings.Repeat(`<imgdir name="x">`, 130) + strings.Repeat(`</imgdir>`, 130),
		`<imgdir>` + strings.Repeat(`<int/>`, 65536) + `</imgdir>`,
		`<imgdir/><imgdir/>`,
		`<imgdir/>trailing data`,
		``,
	} {
		if err := checkBodyXMLBudget([]byte(raw)); !errors.Is(err, ErrMobBodyUnknown) {
			t.Fatal("oversized XML structure passed preflight", err)
		}
	}
	root := t.TempDir()
	var frames strings.Builder
	frames.WriteString(`<imgdir name="stand">`)
	for i := 0; i < 4097; i++ {
		frames.WriteString(bodyFrame(fmt.Sprint(i), 0, 0, 0, 0))
	}
	frames.WriteString(`</imgdir>`)
	bodyFile(t, root, 11, frames.String())
	if _, err := MobBodyDefinition(root, 11); !errors.Is(err, ErrMobBodyUnknown) {
		t.Fatal("empty frames bypassed the traversal budget", err)
	}
}

func TestMobBodyAliasFailuresNeverAuthorizeGuessedGeometry(t *testing.T) {
	for _, target := range []string{"/move/0", "../../move/0", "other.img/move/0", "C:\\move", "0", "1", "../move/absent"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			bodyFile(t, root, 11, `<imgdir name="move">`+bodyFrame("2", -10, -20, 10, 0)+`<uol name="0" value="`+target+`"/><uol name="1" value="0"/></imgdir>`)
			if got, err := MobBodyDefinition(root, 11); !errors.Is(err, ErrMobBodyUnknown) || got.Envelope.Nonempty() {
				t.Fatalf("invalid alias resolved: %+v, %v", got, err)
			}
		})
	}
	root := t.TempDir()
	bodyFile(t, root, 11, `<imgdir name="move">`+bodyFrame("0", -10, -20, 10, 0)+bodyFrame("0", -40, -20, 10, 0)+`</imgdir>`)
	if _, err := MobBodyDefinition(root, 11); !errors.Is(err, ErrMobBodyUnknown) {
		t.Fatal("ambiguous duplicate frame names accepted", err)
	}
}
