package content

import (
	"os"
	"testing"
	"time"
)

func TestSubwaySurvivalMountedV148(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("Subway survival uses the mounted v148 maps")
	}
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	want := SubwaySurvivalRules{Gauge: 100, Decay: 1, HitGain: 1, CoolGain: 10, MissCost: 5, Respawn: 3 * time.Second, DisableSkills: true}
	for _, id := range []int{SubwaySurvivalFirst, SubwaySurvivalSecond, SubwaySurvivalThird} {
		m, err := c.Map(id)
		if err != nil {
			t.Fatal(err)
		}
		if m.Subway == nil || *m.Subway != want {
			t.Fatalf("map %d loaded the wrong v148 survival rules: %+v", id, m.Subway)
		}
		pop := 0
		for _, life := range m.Life {
			if life.Kind != "m" {
				continue
			}
			mob, err := c.Mob(life.TemplateID)
			if err != nil || mob.HP != 1 || !mob.MassacreRemoveOnMiss || mob.MassacreCoolDamage != 200 {
				t.Fatalf("map %d loaded invalid survival monster %d: %v", id, life.TemplateID, err)
			}
			pop++
		}
		if pop != 50 {
			t.Fatalf("map %d expected 50 original spawn slots, got %d", id, pop)
		}
	}
}
