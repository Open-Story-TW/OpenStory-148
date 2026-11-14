package content

import (
	"os"
	"path/filepath"
	"testing"
)

// A data root without the Phantom files is completed by the built-in v148
// copies; a file the root does have is never replaced.
func TestV148SupplementFillsMissingRootFiles(t *testing.T) {
	root := t.TempDir()
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Map(915000000)
	if err != nil {
		t.Fatal(err)
	}
	if spawn, ok := m.Spawn(); !ok || m.OnUserEnter != "PTtutor000" || spawn.X != 262 || len(m.Footholds) == 0 {
		t.Fatalf("bedroom %+v", m)
	}
	hall, err := c.Map(915000100)
	if err != nil || len(hall.Life) != 1 || hall.Life[0].TemplateID != 1402000 {
		t.Fatalf("hall %+v %v", hall.Life, err)
	}
	if _, err = c.Map(150000000); err != nil {
		t.Fatal(err)
	}
	if mob, err := c.Mob(9300498); err != nil || !mob.Invincible {
		t.Fatalf("guard %+v %v", mob, err)
	}
	for _, id := range []int{1050222, 1051272, 1102347, 1070023, 1071035, 1352100} {
		if _, err = ItemDefinition(root, id); err != nil {
			t.Errorf("item %d: %v", id, err)
		}
	}
	if _, err = c.Map(915999999); err == nil {
		t.Fatal("a map neither the root nor the supplement has was found")
	}
	// The root's own file wins.
	dir := filepath.Join(root, "Map.wz", "Map", "Map9")
	if err = os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	own := `<imgdir name="915000200.img"><imgdir name="info"><string name="onUserEnter" value="own"/></imgdir><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="1"/><int name="y" value="2"/></imgdir></imgdir></imgdir>`
	if err = os.WriteFile(filepath.Join(dir, "915000200.img.xml"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, err = c.Map(915000200); err != nil || m.OnUserEnter != "own" {
		t.Fatalf("root file not used: %+v %v", m, err)
	}
}

func TestPhantomTutorialQuestComesFromTheSupplement(t *testing.T) {
	q, err := QuestDefinition("", 25000)
	if err != nil {
		t.Fatal(err)
	}
	if q.Name != "潛入作戰" || q.Start.NPCID != 1402000 || q.Start.Script != "quest/25000.lua" || len(q.Start.Jobs) != 1 || q.Start.Jobs[0] != 2003 || q.CanForfeit {
		t.Fatalf("quest 25000 %+v", q)
	}
}
