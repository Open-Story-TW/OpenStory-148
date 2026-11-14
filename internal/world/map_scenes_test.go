package world

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/internal/scripting"
	"maple-go-server/scripts"
)

type entrySceneHost struct {
	mapID, gender int64
	played        []string
}

func (h *entrySceneHost) Execute(_ context.Context, _ scripting.Identity, c scripting.Command) (scripting.CommandResult, error) {
	switch c.Kind {
	case "gender":
		return scripting.CommandResult{Value: h.gender}, nil
	case "map_id":
		return scripting.CommandResult{Value: h.mapID}, nil
	case "play_scene":
		h.played = append(h.played, c.Text)
		return scripting.CommandResult{Value: true}, nil
	}
	return scripting.CommandResult{}, fmt.Errorf("unexpected %s", c.Kind)
}

// TestEntrySceneScriptsAskForTheirReviewedScene runs every reviewed
// onUserEnter source on each of its maps for both genders: it must ask for
// exactly the scene the host will accept there, and for nothing else.
func TestEntrySceneScriptsAskForTheirReviewedScene(t *testing.T) {
	id := scripting.Identity{SessionID: "entry-scene-session", CharacterID: 7, Generation: 1}
	for script, maps := range entryScenes {
		name := "map/on_user_enter/" + script + ".lua"
		if entrySceneScript(name) != script {
			t.Fatalf("%s does not map back to %s", name, script)
		}
		source, err := scripts.Source(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for mapID := range maps {
			for gender := 0; gender < 2; gender++ {
				want, ok := entryScenePath(script, mapID, gender)
				if !ok {
					t.Fatalf("%s has no scene on %d", script, mapID)
				}
				h := &entrySceneHost{mapID: int64(mapID), gender: int64(gender)}
				e, err := scripting.New(scripting.Options{Host: h})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.Register(name, source); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				s, event, err := e.Start(context.Background(), id, name, scripting.StartOptions{API: "map"})
				if err != nil {
					t.Fatalf("%s on %d: %v", name, mapID, err)
				}
				s.Cancel()
				if event.Kind != "done" || len(h.played) != 1 || h.played[0] != want {
					t.Fatalf("%s on %d gender %d: event=%s played=%v want %s", name, mapID, gender, event.Kind, h.played, want)
				}
			}
		}
	}
	if _, ok := entryScenePath("goLith", 100000000, 0); ok {
		t.Fatal("a scene script ran outside its maps")
	}
	if _, ok := entryScenePath("cygnusTest", 913040011, 0); ok {
		t.Fatal("cygnusTest has no single-transfer scene on 913040011")
	}
}
