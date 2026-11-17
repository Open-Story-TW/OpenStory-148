package world

import (
	"testing"

	"maple-go-server/internal/content"
)

func TestEscapeMazeRoutesStayReachable(t *testing.T) {
	for range 100 {
		state := newEscapeState()
		for room, routes := range state.maze {
			from := content.EscapeMazeFirst + 10*room
			seen := map[int]bool{}
			for _, next := range routes {
				if next == from || !content.EscapeMazeMap(next) || seen[next] {
					t.Fatalf("invalid routes from %d: %v", from, routes)
				}
				if next == content.EscapeMazeLast && room != 4 {
					t.Fatalf("tower 6 reachable from %d", from)
				}
				seen[next] = true
			}
			if room == 0 && !seen[content.EscapeMazeLast-10] || room == 4 && !seen[content.EscapeMazeLast] ||
				room != 0 && !seen[content.EscapeMazeFirst] {
				t.Fatalf("required route missing from %d: %v", from, routes)
			}
		}
	}
}

func TestEscapePrisonRequiresAllFourDoors(t *testing.T) {
	room := &gameplayRoom{reactors: map[int32]*roomReactor{}}
	for oid := int32(1); oid <= 4; oid++ {
		room.reactors[oid] = &roomReactor{def: content.Reactor{TemplateID: content.EscapePrisonDoor}}
	}
	for oid := int32(1); oid <= 4; oid++ {
		if escapeDoorsOpenedLocked(room) {
			t.Fatalf("stage opened with %d doors", oid-1)
		}
		room.reactors[oid].state = 1
	}
	if !escapeDoorsOpenedLocked(room) {
		t.Fatal("stage stayed sealed after opening all four doors")
	}
}
