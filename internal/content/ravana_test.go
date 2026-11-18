package content

import "testing"

func TestRavanaRoutesAreRegisteredWithDistinctAdmission(t *testing.T) {
	story, ok := EventByName(RavanaStoryName)
	if !ok || story.PartyMin != 1 || story.PartyMax != 1 || len(story.Maps) != 1 || story.Maps[0] != RavanaStoryArena {
		t.Fatalf("story route must admit a solo player into its own arena: %+v", story)
	}
	party, ok := EventByName(RavanaName)
	if !ok || party.PartyMin != 2 || len(party.Maps) != 1 || party.Maps[0] != RavanaArena {
		t.Fatalf("party route must retain its separate arena and minimum roster: %+v", party)
	}
}

func TestRavanaCraftingDropDoesNotLeakOutsideGoldenTemple(t *testing.T) {
	for _, mapID := range []int{252010000, 100000000} {
		rows, err := MonsterDrops(8144000, mapID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.TemplateID == RavanaBurning {
				found = true
			}
		}
		if found != (mapID == 252010000) {
			t.Fatalf("Golden Temple crafting drop has wrong map scope in %d", mapID)
		}
	}
}
