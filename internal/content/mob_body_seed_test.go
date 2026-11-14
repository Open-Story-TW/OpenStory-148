package content

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestEmbeddedBodiesCoverEveryStarterMobWithoutExternalWZ(t *testing.T) {
	c, err := NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.mobs) != 9 || len(c.mobBodies) != len(c.mobs) {
		t.Fatal("starter/body coverage mismatch", len(c.mobs), len(c.mobBodies))
	}
	for id := range c.mobs {
		b, err := c.MobBodyBounds(id)
		if err != nil || !b.Envelope.Nonempty() || b.FrameCount < 1 || b.Sources[0].ID != id {
			t.Fatalf("starter monster %d is unavailable: %+v, %v", id, b, err)
		}
		b.Sources[0].SHA256 = "caller mutation"
		again, err := c.MobBodyBounds(id)
		if err != nil || again.Sources[0].SHA256 == "caller mutation" {
			t.Fatal("embedded provenance is mutable through a caller")
		}
	}
	if _, err := c.MobBodyBounds(7654321); err == nil {
		t.Fatal("unknown monster received a guessed body")
	}
}

func TestMountedBodyCannotBeMaskedByEmbeddedBody(t *testing.T) {
	root := t.TempDir()
	bodyFile(t, root, 9300018, `<imgdir name="stand">`+bodyFrame("0", -7, -9, 8, -1)+`</imgdir>`)
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.MobBodyBounds(9300018)
	if err != nil || b.Envelope != (MobBodyRect{-8, -9, 8, -1}) {
		t.Fatal("mounted geometry did not take precedence", b, err)
	}
	if _, err := c.MobBodyBounds(100000); err == nil {
		t.Fatal("missing mounted body silently fell back to a different asset version")
	}
}

func TestEmbeddedBodySeedRejectsCorruptionAndCoverageDrift(t *testing.T) {
	c, err := NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"version", "starter-hash", "missing", "duplicate", "unknown", "empty", "envelope", "source-hash", "source-root", "source-cycle", "trailing"} {
		t.Run(name, func(t *testing.T) {
			var seed MobBodySeed
			if err := json.Unmarshal(mobBodySeed, &seed); err != nil {
				t.Fatal(err)
			}
			first := &seed.Definitions[0]
			switch name {
			case "version":
				seed.Version++
			case "starter-hash":
				seed.StarterSHA256 = "mismatch"
			case "missing":
				seed.Definitions = seed.Definitions[1:]
			case "duplicate":
				seed.Definitions[1] = *first
			case "unknown":
				first.ID = 7654321
			case "empty":
				first.Bounds.Envelope = MobBodyRect{}
			case "envelope":
				first.Bounds.Envelope.Left--
			case "source-hash":
				first.Bounds.Sources[0].SHA256 = "bad"
			case "source-root":
				first.Bounds.Sources[0].ID++
			case "source-cycle":
				first.Bounds.Sources = append(first.Bounds.Sources, first.Bounds.Sources[0])
			}
			data, err := json.Marshal(seed)
			if err != nil {
				t.Fatal(err)
			}
			if name == "trailing" {
				data = append(data, []byte(` {}`)...)
			}
			if _, err := loadEmbeddedMobBodies(data, c.mobs); err == nil {
				t.Fatal("corrupt seed accepted")
			}
		})
	}
}

func TestEmbeddedBodyMatchesMountedWZ(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("set MAPLEGO_WZ_ROOT to audit embedded geometry against WZ")
	}
	c, err := NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	for id := range c.mobs {
		mounted, err := MobBodyDefinition(root, id)
		if err != nil {
			t.Fatal(err)
		}
		embedded, _ := c.MobBodyBounds(id)
		if !reflect.DeepEqual(mounted, embedded) {
			t.Fatalf("embedded monster %d geometry/provenance differs from WZ", id)
		}
	}
}
