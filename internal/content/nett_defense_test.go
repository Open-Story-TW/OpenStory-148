package content

import (
	"os"
	"testing"
	"time"
)

func TestNettDefenseMountedV148(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("Nett defense uses the mounted v148 maps")
	}
	c, err := NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct{ mapID, first, last int }{
		{NettNormalBase, 9305400, 9305422},
		{NettHardBase, 9305500, 9305522},
	} {
		m, err := c.Map(mode.mapID)
		if err != nil {
			t.Fatal(err)
		}
		d := m.NettDefense
		if d == nil || d.ObeliskX != -858 || d.Lanes[2] != (NettLane{910, -220}) ||
			d.MPBonus != 10 || d.MPEvery != 5*time.Second {
			t.Fatalf("field %d loaded the wrong defense rules", mode.mapID)
		}
		up, ok := m.Portal("up01")
		if !ok || up.TargetMap != mode.mapID || up.TargetName != "up02" {
			t.Fatalf("field %d inherited an out-of-instance lane portal: %+v", mode.mapID, up)
		}
		last := d.Waves[NettWaves-1]
		if len(d.Waves[0]) != 32 || d.Waves[0][0] != (NettSpawn{mode.first, 0, time.Second}) ||
			len(last) != 12 || last[11] != (NettSpawn{mode.last, 2, 8 * time.Second}) {
			t.Fatalf("field %d loaded the wrong monster wave order", mode.mapID)
		}
		for _, w := range d.Waves {
			for _, spawn := range w {
				mob, err := c.Mob(spawn.MobID)
				if err != nil || mob.ID < mode.first || mob.ID > mode.last || mob.DefenseLifePoint < 1 {
					t.Fatalf("field %d loaded invalid template %d: %v", mode.mapID, spawn.MobID, err)
				}
			}
		}
	}
}
