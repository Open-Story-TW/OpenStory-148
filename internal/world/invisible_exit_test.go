package world

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func invisibleExitFixture(t *testing.T, portalType, target int) (*Server, *liveCharacter, *gameplayTestSink, *[]domain.Character) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "Map.wz", "Map", "Map1")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Source WZ103000003 out00 is type1 at(201,-266), targeting
	// 103000000/in03. Keep these exact portal identities in a small fixture.
	portal := func(name string, kind, x, y, tm int, tn string) string {
		return fmt.Sprintf(`<imgdir name="1"><string name="pn" value="%s"/><int name="pt" value="%d"/><int name="x" value="%d"/><int name="y" value="%d"/><int name="tm" value="%d"/><string name="tn" value="%s"/></imgdir>`, name, kind, x, y, tm, tn)
	}
	for id, exit := range map[int]string{103000003: portal("out00", portalType, 201, -266, target, "in03"), 103000000: portal("in03", 2, 211, -145, 103000003, "out00")} {
		xml := `<imgdir name="map"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir>` + exit + `</imgdir></imgdir>`
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%09d.img.xml", id)), []byte(xml), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	g, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	g.catalog = catalog
	saved := []domain.Character{}
	s := &Server{gameplay: g, maps: newMapSpace(800), drops: newDropSpace(), online: map[int64]domain.CharacterSession{}, repo: gameplayTestRepo{save: func(_ domain.CharacterSession, c domain.Character) error { saved = append(saved, c); return nil }}}
	live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, Level: 30, Job: 130, MapID: 103000003, X: 201, Y: -262, HP: 50, MaxHP: 100, MP: 50, MaxMP: 100, STR: 4, DEX: 4, INT: 4, LUK: 4, Revision: 1}}}
	sink := &gameplayTestSink{}
	seedBoundScene(t, s, live, sink)
	if err = s.enterGameplay(1, live, sink); err != nil {
		t.Fatal(err)
	}
	return s, live, sink, &saved
}

func TestInvisibleJobRoomExitPersistsThenPublishesDestination(t *testing.T) {
	s, live, sink, saved := invisibleExitFixture(t, 1, 103000000)
	if err := s.portalGameplay(context.Background(), 1, live, sink, protocol.PortalRequest{TargetMap: -1, Name: "out00"}); err != nil {
		t.Fatal(err)
	}
	if len(*saved) != 1 || (*saved)[0].MapID != 103000000 || live.session.Character.MapID != 103000000 || live.session.Character.X != 211 || live.session.Character.Y != -145 || sink.count(protocol.SendSetField) != 1 {
		t.Fatalf("type1 exit did not persist/publish exactly once: saves=%+v live=%+v", *saved, live.session.Character)
	}
}

func TestInvisibleExitKeepsPortalAuthorityChecks(t *testing.T) {
	for _, which := range []string{"far", "dead", "forged_target", "missing_name", "spawn", "door", "script_type", "no_target"} {
		t.Run(which, func(t *testing.T) {
			kind, target := 1, 103000000
			switch which {
			case "spawn":
				kind = 0
			case "door":
				kind = 6
			case "script_type":
				kind = 7
			case "no_target":
				target = 999999999
			}
			s, live, sink, saved := invisibleExitFixture(t, kind, target)
			request := protocol.PortalRequest{TargetMap: -1, Name: "out00"}
			switch which {
			case "far":
				live.session.Character.X = 1000
			case "dead":
				live.session.Character.HP = 0
			case "forged_target":
				request.TargetMap = 103000000
			case "missing_name":
				request.Name = "missing"
			}
			if err := s.portalGameplay(context.Background(), 1, live, sink, request); err != nil {
				t.Fatal(err)
			}
			if len(*saved) != 0 || live.session.Character.MapID != 103000003 || sink.count(protocol.SendSetField) != 0 {
				t.Fatal("invalid invisible exit changed scene")
			}
		})
	}
}
