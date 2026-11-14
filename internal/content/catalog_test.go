package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedMapleIslandRoute(t *testing.T) {
	c, err := NewCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{10000, 20000, 30000, 30001, 40000, 50000, 1000000} {
		m, err := c.Map(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m.Spawn(); !ok {
			t.Fatalf("map%d has no spawn", id)
		}
		for _, p := range m.Portals {
			if p.Type == 2 && p.Script == "" && p.TargetMap != 999999999 {
				to, err := c.Map(p.TargetMap)
				if err != nil {
					t.Fatalf("map%d portal%q: %v", id, p.Name, err)
				}
				if _, ok := to.Portal(p.TargetName); !ok {
					t.Fatalf("map%d missing return portal %q", to.ID, p.TargetName)
				}
			}
		}
	}
	m, _ := c.Map(10000)
	out, ok := m.Portal("out00")
	if !ok || out.TargetMap != 20000 || out.X != 1077 || out.Y != 480 {
		t.Fatalf("starter portal differs from WZ: %+v", out)
	}
	combat, _ := c.Map(40000)
	mobs := 0
	for _, life := range combat.Life {
		if life.Kind == "m" {
			mobs++
			stats, e := c.Mob(life.TemplateID)
			if e != nil || stats.HP <= 0 {
				t.Fatalf("missing mob %+v: %v", life, e)
			}
		}
	}
	if mobs != 3 {
		t.Fatalf("tutorial mob spawns=%d want3", mobs)
	}
}

func TestExternalMapXMLAndBadContent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Map.wz", "Map", "Map1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body := `<imgdir name="123456789.img"><imgdir name="info"><int name="returnMap" value="10000"/></imgdir><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="12"/><int name="y" value="34"/></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(dir, "123456789.img.xml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	c, _ := NewCatalog(root)
	m, err := c.Map(123456789)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := m.Spawn()
	if !ok || p.X != 12 || p.Y != 34 || m.ReturnMap != 10000 {
		t.Fatalf("bad external map: %+v", m)
	}
	if _, err = c.Map(-1); err == nil {
		t.Fatal("accepted negative map")
	}
	if _, err = c.Map(999999999); err == nil {
		t.Fatal("accepted sentinel map")
	}
	if ExperienceForLevel(1) != 15 || ExperienceForLevel(2) != 34 {
		t.Fatal("experience table differs from Java")
	}
}

func TestMobAnimationLinkDoesNotOverrideOwnCombatStats(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Mob.wz")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// A linked image may be missing entirely: combat attributes are complete
	// on the original monster and loading them must not read that other file.
	body := `<imgdir name="7654321.img"><imgdir name="info"><string name="link" value="7654322"/><int name="maxHP" value="35"/><int name="exp" value="6"/><int name="level" value="4"/><int name="PADamage" value="10"/><int name="PDRate" value="5"/><int name="onlyNormalAttack" value="1"/></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(dir, "7654321.img.xml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Mob(7654321)
	if err != nil || m.HP != 35 || m.EXP != 6 || m.Level != 4 || m.PAD != 10 || m.PDRate != 5 || !m.OnlyNormalAttack {
		t.Fatalf("animation link replaced combat stats: %+v %v", m, err)
	}
	// The embedded training variants must retain their own stats as well.
	for id, want := range map[int]Mob{
		100005: {HP: 35, EXP: 6, Level: 4, PAD: 10},
		100002: {HP: 35, EXP: 6, Level: 4, PAD: 10},
		100007: {HP: 80, EXP: 12, Level: 7, PAD: 24},
	} {
		m, err = c.Mob(id)
		if err != nil || m.HP != want.HP || m.EXP != want.EXP || m.Level != want.Level || m.PAD != want.PAD {
			t.Fatalf("embedded training mob%d: %+v %v", id, m, err)
		}
	}
}

func TestMapFlyFlagFollowsWZInfo(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("flyable fields load from MAPLEGO_WZ_ROOT")
	}
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]bool{200110000: true, 240080000: true, 200090500: true, 100000000: false, 10000: false} {
		m, err := c.Map(id)
		if err != nil || m.Fly != want {
			t.Fatalf("map %d fly=%v want %v (%v)", id, m.Fly, want, err)
		}
	}
}
