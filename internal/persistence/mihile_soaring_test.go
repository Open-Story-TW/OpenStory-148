package persistence

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestMihileSoaringDebitsElapsedTicksAndEnds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	for _, field := range []struct{ dir, image, flag string }{
		{"Map2", "200110000", "1"}, {"Map1", "104010000", "0"},
	} {
		dir := filepath.Join(root, "Map.wz", "Map", field.dir)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		xml := `<imgdir name="` + field.image + `.img"><imgdir name="info"><int name="fly" value="` + field.flag + `"/></imgdir><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/><int name="x" value="0"/><int name="y" value="0"/></imgdir></imgdir></imgdir>`
		if err := os.WriteFile(filepath.Join(dir, field.image+".img.xml"), []byte(xml), 0600); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Unix(42, 0)
	tick := at.Add(time.Second)
	b := domain.SkillBuff{ID: skills.MihileSoaringID, Level: 1, Stat: "SOARING", Value: 1, NextTick: tick}
	c := domain.Character{MapID: 200110000, MP: 4}
	advanced, removed, err := advanceMihileSoaring(&c, b, at.Add(3*time.Second))
	if err != nil || removed || c.MP != 1 || !advanced.NextTick.Equal(at.Add(4*time.Second)) {
		t.Fatalf("elapsed steps: mp=%d row=%+v removed=%v err=%v", c.MP, advanced, removed, err)
	}
	_, removed, err = advanceMihileSoaring(&c, advanced, advanced.NextTick)
	if err != nil || !removed || c.MP != 0 {
		t.Fatalf("depleted: mp=%d removed=%v err=%v", c.MP, removed, err)
	}
	c = domain.Character{MapID: 104010000, MP: 4}
	_, removed, err = advanceMihileSoaring(&c, b, tick)
	if err != nil || !removed || c.MP != 4 {
		t.Fatalf("non-flight map: mp=%d removed=%v err=%v", c.MP, removed, err)
	}
}
