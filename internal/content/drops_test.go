package content

import "testing"

func TestMonsterDropCatalogRetainsLegacyRows(t *testing.T) {
	rows, err := MonsterDrops(100100, 40000)
	if err != nil {
		t.Fatal(err)
	}
	shell, globalNX := false, false
	for _, row := range rows {
		if row.ID == 13055 && !row.Global {
			shell = row.TemplateID == 4000019 && row.Chance == 600000 && row.Minimum == 1 && row.Maximum == 1
		}
		if row.Global && row.TemplateID == 0 {
			globalNX = true
		}
	}
	if !shell || !globalNX {
		t.Fatal("source shell or global NX policy was lost")
	}
	rows[0].TemplateID = -1
	copy, err := MonsterDrops(100100, 40000)
	if err != nil || copy[0].TemplateID < 0 {
		t.Fatal("caller mutated the shared drop catalog")
	}
}
