package content

import "testing"

func TestMuLungBossSpawnPreservesWZLife(t *testing.T) {
	for _, tc := range []struct {
		mapID, bossID, respawn int
	}{
		{250010304, 7220000, 2100},
		{250010503, 7220002, 1800},
	} {
		regular := Life{Index: 6, Kind: "m", TemplateID: 6130203}
		m := Map{ID: tc.mapID, Life: []Life{regular}}
		addMuLungBossSpawn(&m)
		if len(m.Life) != 2 || m.Life[0] != regular {
			t.Fatalf("map %d changed normal WZ life: %+v", tc.mapID, m.Life)
		}
		boss := m.Life[1]
		if boss.Index != 7 || boss.TemplateID != tc.bossID || boss.Kind != "m" || boss.RespawnSeconds != tc.respawn || boss.Foothold == 0 {
			t.Fatalf("map %d invalid boss spawn: %+v", tc.mapID, boss)
		}
		addMuLungBossSpawn(&m)
		if len(m.Life) != 2 {
			t.Fatalf("map %d duplicated existing boss: %+v", tc.mapID, m.Life)
		}
	}
}
