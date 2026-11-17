package world

import (
	"testing"

	"maple-go-server/internal/content"
)

func TestPinkBeanWavesPreserveTheWZStatuePools(t *testing.T) {
	g := &gameplaySpace{nextID: 1000000}
	stage := &gameplayRoom{mobs: map[int32]*gameplayMob{}, pinkBean: &pinkBeanBattle{phase: -1, stats: map[int]content.Mob{}}}
	bodyHP := map[int]int64{
		8820003: 300000000, 8820004: 300000000, 8820005: 450000000, 8820006: 450000000,
		8820015: 300000000, 8820016: 300000000, 8820017: 450000000, 8820018: 450000000,
		8820002: 600000000, pinkBeanBody: 2100000000,
	}
	for id := 8820000; id <= 8820027; id++ {
		stage.pinkBean.stats[id] = content.Mob{ID: id, HP: max(bodyHP[id], 1)}
	}
	totals := []int64{0, 0, 300000000, 600000000, 1050000000, 1500000000, 2100000000, 2100000000}
	for phase, supportedHP := range totals {
		old := stage.pinkBean.sceneMobs
		mobs, targets, ok := g.pinkBeanWaveObjects(stage.pinkBean, phase)
		if !ok {
			t.Fatalf("phase %d could not spawn", phase)
		}
		installPinkBeanWave(stage, phase, mobs, targets)
		for _, mob := range old {
			if stage.mobs[mob.objectID] != nil {
				t.Fatalf("phase %d retained object %d from an earlier wave", phase, mob.objectID)
			}
		}
		var hp int64
		for _, mob := range stage.mobs {
			if mob.fake {
				if targets[mob.objectID] != nil || mob.lethalHP() != 0 {
					t.Fatalf("phase %d permitted attacking scenery %d", phase, mob.stats.ID)
				}
				continue
			}
			if targets[mob.objectID] != mob {
				t.Fatalf("phase %d did not account for statue %d", phase, mob.stats.ID)
			}
			hp += mob.hp
		}
		if hp != supportedHP {
			t.Fatalf("phase %d: got %d HP, want %d", phase, hp, supportedHP)
		}
	}
	if len(stage.mobs) != 1 || len(stage.pinkBean.targets) != 1 {
		t.Fatal("the final phase must be the living Pink Bean alone")
	}
}
