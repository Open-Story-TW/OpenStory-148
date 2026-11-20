package content

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDojoFloorAndBossMapping(t *testing.T) {
	rest := map[int]bool{6: true, 12: true, 18: true, 24: true, 30: true, 36: true, 42: true}
	stages, last := 0, 0
	for floor := 1; floor <= DojoFloors; floor++ {
		if DojoRestFloor(floor) != rest[floor] {
			t.Fatalf("floor %d rest=%v", floor, DojoRestFloor(floor))
		}
		boss, ok := DojoBoss(floor)
		if rest[floor] {
			if ok || DojoStage(floor) != 0 {
				t.Fatalf("rest floor %d has boss %d", floor, boss)
			}
			continue
		}
		stages++
		if DojoStage(floor) != stages || !ok || boss != DojoFirstBoss+stages-1 {
			t.Fatalf("floor %d: stage %d boss %d, want stage %d", floor, DojoStage(floor), boss, stages)
		}
		last = boss
	}
	if stages != DojoStages || last != 9305139 {
		t.Fatalf("stages %d last boss %d", stages, last)
	}
	for _, c := range []struct{ mapID, floor, room int }{
		{925020100, 1, 0}, {925020109, 1, 9}, {925020600, 6, 0}, {925024705, 47, 5},
	} {
		floor, room, ok := DojoFloorOf(c.mapID)
		if !ok || floor != c.floor || room != c.room || DojoFloorMap(floor, room) != c.mapID {
			t.Fatalf("%d -> %d/%d %v", c.mapID, floor, room, ok)
		}
	}
	for _, id := range []int{DojoLobby, DojoHall, DojoExit, 925020003, 925020110, 925024800, 925030100, 925019999} {
		if DojoField(id) || DojangField(id) {
			t.Fatalf("%d is not a normal-mode floor", id)
		}
	}
	if DojangField(925020600) || !DojoField(925020600) || !DojangField(925020500) {
		t.Fatal("rest floor 6 must be a dojo field without the Dojang skills")
	}
}

func TestDojoInstanceEvents(t *testing.T) {
	seen := map[int]bool{}
	for room := 0; room < DojoRooms; room++ {
		name := DojoEventName(room)
		e, ok := EventByName(name)
		if got, known := DojoEventRoom(name); !ok || !known || got != room || !DojoEvent(name) {
			t.Fatalf("room %d event %q missing", room, name)
		}
		if e.LobbyMap != DojoHall || e.ExitMap != DojoExit || e.Seconds != DojoFloorSeconds(1) || len(e.Maps) != DojoFloors ||
			len(e.Spawns) != DojoStages || !e.AllowSolo || e.PartyMin != 1 || e.Keep != 1 {
			t.Fatalf("room %d event %+v", room, e)
		}
		for i, id := range e.Maps {
			if seen[id] || id != DojoFloorMap(i+1, room) {
				t.Fatalf("room %d floor %d map %d", room, i+1, id)
			}
			seen[id] = true
			if owner, ok := EventByMap(id); !ok || owner.Name != name {
				t.Fatalf("map %d owner %q", id, owner.Name)
			}
		}
		for _, spawn := range e.Spawns {
			floor, r, _ := DojoFloorOf(spawn.MapID)
			if boss, _ := DojoBoss(floor); r != room || spawn.Life.TemplateID != boss || spawn.Life.RespawnSeconds != -1 {
				t.Fatalf("room %d spawn %+v", room, spawn)
			}
		}
	}
	for _, bad := range []string{"MuLungDojo", "MuLungDojo10", "MuLungDojoA", "Dojo0"} {
		if DojoEvent(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// TestDojoMatchesV148Maps checks the floor rules and the boss spawn point
// against a full v148 WZ XML export (MAPLEGO_V148_WZ) when one is available.
func TestDojoMatchesV148Maps(t *testing.T) {
	root := os.Getenv("MAPLEGO_V148_WZ")
	if _, err := os.Stat(filepath.Join(root, "Map.wz/Map/Map9/925020100.img.xml")); root == "" || err != nil {
		t.Skip("MAPLEGO_V148_WZ is not a v148 WZ export")
	}
	type node struct {
		XMLName  xml.Name
		Name     string `xml:"name,attr"`
		Value    string `xml:"value,attr"`
		Children []node `xml:",any"`
	}
	child := func(n *node, name string) *node {
		for i := range n.Children {
			if n.Children[i].Name == name {
				return &n.Children[i]
			}
		}
		return nil
	}
	load := func(mapID int) *node {
		data, err := os.ReadFile(filepath.Join(root, "Map.wz/Map/Map9", strconv.Itoa(mapID)+".img.xml"))
		if err != nil {
			t.Fatal(err)
		}
		var n node
		if err = xml.Unmarshal(data, &n); err != nil {
			t.Fatal(err)
		}
		return &n
	}
	for floor := 1; floor <= DojoFloors; floor++ {
		for room := 0; room < DojoRooms; room++ {
			n := load(DojoFloorMap(floor, room))
			info := child(n, "info")
			fieldType := ""
			if f := child(info, "fieldType"); f != nil {
				fieldType = f.Value
			}
			if (fieldType == "14") != DojangField(DojoFloorMap(floor, room)) {
				t.Fatalf("floor %d room %d fieldType %q", floor, room, fieldType)
			}
			if forced := child(info, "forcedReturn"); forced == nil || forced.Value != strconv.Itoa(DojoExit) {
				t.Fatalf("floor %d room %d forcedReturn", floor, room)
			}
		}
		base := load(DojoFloorMap(floor, 0))
		portals := child(base, "portal")
		names := map[string]string{}
		for _, p := range portals.Children {
			pn, script := child(&p, "pn"), child(&p, "script")
			if script != nil {
				names[pn.Value] = script.Value
			}
		}
		if names["out001"] != "dojang_next" || DojangField(DojoFloorMap(floor, 0)) && names["out00"] != "dojang_up" {
			t.Fatalf("floor %d portals %v", floor, names)
		}
		if p0 := child(child(portals, "0"), "pn"); p0 == nil || p0.Value != "sp" {
			t.Fatalf("floor %d portal 0 is not sp", floor)
		}
	}
	for stage := 0; stage < DojoStages; stage++ {
		data, err := os.ReadFile(filepath.Join(root, "Mob.wz", strconv.Itoa(DojoFirstBoss+stage)+".img.xml"))
		if err != nil {
			t.Fatalf("boss %d: %v", DojoFirstBoss+stage, err)
		}
		var n node
		if err = xml.Unmarshal(data, &n); err != nil {
			t.Fatal(err)
		}
		if boss := child(child(&n, "info"), "boss"); boss == nil || boss.Value != "1" {
			t.Fatalf("mob %d is not a boss", DojoFirstBoss+stage)
		}
	}
	// The spawn foothold is the lower ground right of the start portals.
	n := load(DojoFloorMap(1, 0))
	var seven *node
	for _, layer := range child(n, "foothold").Children {
		for _, group := range layer.Children {
			if f := child(&group, "7"); f != nil {
				seven = f
			}
		}
	}
	if seven == nil || child(seven, "x1").Value != "235" || child(seven, "x2").Value != "502" || child(seven, "y1").Value != "7" {
		t.Fatalf("foothold 7 moved: %+v", seven)
	}
}

func TestDojoFloorTimesFollowTheTierTable(t *testing.T) {
	for floor, want := range map[int]int{1: 300, 5: 300, 7: 360, 11: 360, 13: 420, 19: 480, 25: 540, 31: 600, 35: 600,
		37: 900, 38: 900, 47: 900, 6: DojoRestFloorSeconds, 42: DojoRestFloorSeconds, 0: 0, 48: 0} {
		if got := DojoFloorSeconds(floor); got != want {
			t.Errorf("floor %d: %d s, want %d", floor, got, want)
		}
	}
}
