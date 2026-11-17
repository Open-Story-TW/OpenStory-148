package world

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

type papulatusFixtureRepo struct {
	gameplayTestRepo
	consumed []int32
}

func (r *papulatusFixtureRepo) ConsumePapulatusFragment(_ context.Context, _ domain.CharacterSession, id int32) error {
	r.consumed = append(r.consumed, id)
	return nil
}

func TestPapulatusOfferingAndGateReset(t *testing.T) {
	ctx := context.Background()
	fixture := eventTestFixture(t)
	s := fixture.server
	repo := &papulatusFixtureRepo{gameplayTestRepo: s.repo.(gameplayTestRepo)}
	s.repo = repo
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	dir := filepath.Join(root, "Map.wz", "Map", "Map2")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{content.PapulatusLobby, content.PapulatusArena} {
		data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir></imgdir>`, id)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for id := papulatusCrystal; id <= papulatusBody; id++ {
		data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"><int name="maxHP" value="10000"/></imgdir></imgdir>`, id)
		if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	party := domain.PartySnapshot{ID: 50, LeaderID: 1, Members: []domain.PartyMember{{CharacterID: 1}, {CharacterID: 2}}}
	fixture.join(t, 1, 100, content.PapulatusLobby, party)
	if reason, _, _, err := s.eventStart(ctx, "Papulatus", 1, fixture.lives[1], fixture.sinks[1]); reason != "party_size" || err != nil {
		t.Fatalf("solo entry: %q %v", reason, err)
	}
	fixture.join(t, 2, 100, content.PapulatusLobby, party)
	if reason, _, _, err := s.eventStart(ctx, "Papulatus", 1, fixture.lives[1], fixture.sinks[1]); reason != "" || err != nil {
		t.Fatalf("party entry: %q %v", reason, err)
	}

	key := mapKey{channel: 1, mapID: content.PapulatusArena}
	room := s.gameplay.rooms[key]
	lobby := s.gameplay.rooms[mapKey{channel: 1, mapID: content.PapulatusLobby}]
	fragment := domain.ItemDrop{ObjectID: 123, ChannelID: 1, OwnerID: 1, MapID: key.mapID, TemplateID: content.PapulatusFragment,
		Quantity: 1, X: content.PapulatusOfferLeft - 1, Y: -538}
	actor := partySession(fixture.lives[1])
	s.offerPapulatusFragment(ctx, actor, fragment)
	if len(repo.consumed) != 0 || room.papulatus.altar.state != 0 {
		t.Fatal("an item outside the fracture moved its reactor")
	}
	fragment.X++
	s.offerPapulatusFragment(ctx, actor, fragment)
	fragment.ObjectID++
	s.offerPapulatusFragment(ctx, actor, fragment)
	if len(repo.consumed) != 1 || room.papulatus.altar.state != 1 || room.papulatus.form != papulatusCrystal {
		t.Fatal("the fracture did not start exactly one encounter")
	}
	for _, gate := range lobby.reactors {
		if gate.state != 1 {
			t.Fatal("lobby mechanism did not reflect the activated fracture")
		}
	}
	inst := s.eventOf(1)
	for id := papulatusCrystal; id <= papulatusBody; id++ {
		if room.papulatus.form != id || inst.cleared {
			t.Fatalf("unexpected phase before defeating mob %d", id)
		}
		s.papulatusPartDefeated(key, 1, id)
	}
	if !inst.cleared {
		t.Fatal("the last form did not clear the event")
	}
	s.eventEnd(inst)
	fixture.settle(t, func() bool {
		s.events.mu.Lock()
		defer s.events.mu.Unlock()
		return s.events.running[eventKey{1, "Papulatus"}] == nil
	})
	for _, gate := range lobby.reactors {
		if gate.state != 0 {
			t.Fatal("lobby gate did not reset when the party left")
		}
	}
	if reason, _, _, err := s.eventStart(ctx, "Papulatus", 1, fixture.lives[1], fixture.sinks[1]); reason != "" || err != nil {
		t.Fatalf("next attempt: %q %v", reason, err)
	}
	if next := s.gameplay.rooms[key]; next == room || next.papulatus.altar.state != 0 || len(next.mobs) != 0 {
		t.Fatal("the next party received the previous arena")
	}
}
