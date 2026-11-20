package content

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSummonAttackInfoKeepsNumericWZStrings(t *testing.T) {
	var raw wzNode
	data := `<imgdir name="35111009"><imgdir name="summon"><imgdir name="attack1"><imgdir name="info"><string name="attackCount" value="2"/><int name="mobCount" value="4"/><string name="effect" value="laser"/></imgdir></imgdir></imgdir></imgdir>`
	if err := xml.Unmarshal([]byte(data), &raw); err != nil {
		t.Fatal(err)
	}
	got := skillFromNode(35111009, &raw).SummonAttack
	if !reflect.DeepEqual(got, map[string]int{"attackCount": 2, "mobCount": 4}) {
		t.Fatalf("satellite attack info: %+v", got)
	}
}

// The embedded seed and live WZ parsing must agree on lt/rb and info.
func TestSkillRangeAndInfoSeedMatchesWZ(t *testing.T) {
	seed, err := SkillDefinition("", 1301007)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := seed.RangeAt(20)
	if !ok || r != (SkillRange{LT: [2]int{-300, -200}, RB: [2]int{300, 200}}) || seed.Info["massSpell"] != 1 {
		t.Fatalf("hyper body seed %+v %v", r, seed.Info)
	}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	configured := root != ""
	if !configured {
		root = filepath.Join("..", "..", "..", "MapleStory_Legacy_Server", "wz")
	}
	if _, err = os.Stat(filepath.Join(root, "Skill.wz", "130.img.xml")); err != nil {
		if !configured && os.IsNotExist(err) {
			t.Skipf("optional Java Legacy WZ export not present at %s", root)
		}
		t.Fatalf("cannot read skill range WZ source at %s: %v", root, err)
	}
	ids := []int{1301007, 1301008, 2301002}
	// Gaviota 5211002 belongs to the stock Legacy book. The original v148
	// export omits it, so compare its seeded geometry on the Legacy source.
	if stockSkillBook(filepath.Join(root, "Skill.wz", "521.img.xml"), "521.img.xml") {
		ids = append(ids, 5211002)
	}
	for _, id := range ids {
		a, err := SkillDefinition("", id)
		if err != nil {
			t.Fatalf("seed skill %d: %v", id, err)
		}
		b, err := SkillDefinition(root, id)
		if err != nil || !reflect.DeepEqual(a.Range, b.Range) || !reflect.DeepEqual(a.LevelRanges, b.LevelRanges) || !reflect.DeepEqual(a.Info, b.Info) || !reflect.DeepEqual(a.SummonAttack, b.SummonAttack) {
			t.Fatalf("skill %d seed %+v %v wz %+v %v %v", id, a.Range, a.Info, b.Range, b.Info, err)
		}
	}
}
