package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJudgementBrackets(t *testing.T) {
	for _, c := range []struct {
		level, room int
	}{
		{20, 0}, {21, JudgementRoomFirst}, {30, JudgementRoomFirst},
		{31, JudgementRoomFirst + 1}, {50, JudgementRoomFirst + 1},
		{51, JudgementRoomFirst + 2}, {70, JudgementRoomFirst + 2},
		{71, JudgementRoomFirst + 3}, {90, JudgementRoomFirst + 3},
		{91, JudgementRoomFirst + 4}, {200, JudgementRoomFirst + 4},
	} {
		if got := JudgementRoomForLevel(c.level); got != c.room {
			t.Errorf("level %d uses field %d, want %d", c.level, got, c.room)
		}
	}
}

func TestJudgementMapDoesNotSharePinkBeanLifeOrPortals(t *testing.T) {
	arena := Map{ID: JudgementArena, ReturnMap: 270050300, OnUserEnter: "PinkBeen_before",
		Portals: []Portal{{ID: 0, Name: "sp"}, {ID: 1, Name: "out", Type: 2}},
		Life:    []Life{{Kind: "m", TemplateID: 8820001}}}
	catalog := &Catalog{maps: map[int]Map{JudgementArena: arena}}
	for key := JudgementRoomFirst; key < JudgementRoomFirst+JudgementRoomCount; key++ {
		room, err := catalog.Map(key)
		if err != nil {
			t.Fatal(err)
		}
		if room.ID != key || room.ReturnMap != JudgementLobby || len(room.Life) != 0 || len(room.Portals) != 1 || room.OnUserEnter != "" {
			t.Fatalf("private bracket has public scene content: %+v", room)
		}
	}
	current, err := catalog.Map(JudgementArena)
	if err != nil || current.OnUserEnter != arena.OnUserEnter || len(current.Life) != 1 || len(current.Portals) != 2 {
		t.Fatalf("public Pink Bean arena mutated: %+v, %v", current, err)
	}
}

func TestJudgementCombatUsesRosterHPAndExperience(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	mobRoot := filepath.Join(root, "Mob.wz")
	if err := os.Mkdir(mobRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, xp := range map[string]string{"9300152": "23000", "8141100": "12000"} {
		xml := `<imgdir><imgdir name="info"><int name="level" value="80"/><int name="maxHP" value="809000"/><int name="exp" value="` + xp + `"/></imgdir></imgdir>`
		if err := os.WriteFile(filepath.Join(mobRoot, name+".img.xml"), []byte(xml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct {
		mapID, mobID int
		hp, exp      int64
	}{
		{JudgementRoomFirst + 4, 9300152, 2_000_000, 23000},
		{JudgementRoomFirst + 4, 8141100, 809000, 3000},
		{JudgementArena, 9300152, 809000, 23000},
	} {
		got, err := CombatExperienceMetadata(root, fixture.mapID, fixture.mobID)
		if err != nil || got.MaximumHP != fixture.hp || got.Experience != fixture.exp {
			t.Fatalf("field %d mob %d: rules %+v, err %v", fixture.mapID, fixture.mobID, got, err)
		}
	}
}
