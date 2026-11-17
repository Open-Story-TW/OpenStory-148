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

func TestHorntailRequiresBothHeadsAndSixCrystalHits(t *testing.T) {
	ctx := context.Background()
	f := eventTestFixture(t)
	s := f.server
	event, _ := content.EventByName("Horntail")
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	dir := filepath.Join(root, "Map.wz", "Map", "Map2")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range append(append([]int{}, event.Maps...), event.LobbyMap) {
		data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir></imgdir>`, id)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for id := 8810000; id <= 8810026; id++ {
		data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"><int name="maxHP" value="10000"/></imgdir></imgdir>`, id)
		if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	party := domain.PartySnapshot{ID: 50, LeaderID: 1}
	for id := int64(1); id <= 6; id++ {
		party.Members = append(party.Members, domain.PartyMember{CharacterID: id})
	}
	for _, member := range party.Members {
		f.join(t, member.CharacterID, 120, event.LobbyMap, party)
	}
	s.parties.addExpeditionParty(&expedition{id: 8, kind: event.ExpeditionType, leader: 1, snapshots: map[int64]domain.PartySnapshot{}}, party)
	if reason, _, _, err := s.eventStart(ctx, event.Name, 1, f.lives[1], f.sinks[1]); err != nil || reason != "" {
		t.Fatalf("start: %q %v", reason, err)
	}
	goIn := content.Portal{Name: "next00", Script: "hontale_BR"}
	for i := 0; i < len(event.Maps)-1; i++ {
		from, to := event.Maps[i], event.Maps[i+1]
		key := mapKey{1, from}
		character := partySession(f.lives[1])
		if handled, err := s.horntailStagePortal(ctx, character, f.lives[1], f.sinks[1], goIn); !handled || err != nil || f.mapOf(1) != from {
			t.Fatalf("stage %d let the party skip a head: %v %v", from, handled, err)
		}
		s.beginHorntailStage(key, 1)
		room := s.gameplay.rooms[key]
		head := room.horntail.parts[0]
		f.settle(t, func() bool {
			s.gameplay.mu.Lock()
			defer s.gameplay.mu.Unlock()
			if room.mobs[head.objectID] == nil {
				return false
			}
			head.hp = 0
			return true
		})
		s.horntailDefeated(ctx, &combatTicket{key: key, actorSession: character}, domain.CombatTarget{ObjectID: head.objectID})
		if handled, err := s.horntailStagePortal(ctx, character, f.lives[1], f.sinks[1], goIn); !handled || err != nil || f.mapOf(6) != to {
			t.Fatalf("the cleared head did not advance the whole roster: %v %v", handled, err)
		}
	}
	key := mapKey{1, event.Maps[2]}
	field := s.gameplay.rooms[key].horntail
	actor := partySession(f.lives[1]).Character
	actor.X, actor.Y = field.crystal.def.X, field.crystal.def.Y
	for i := 1; i <= 5; i++ {
		if !s.hitHorntailCrystal(1, actor, field.crystal.objectID) || field.crystal.state != i || field.summoned {
			t.Fatalf("crystal opened before its sixth hit: %d", i)
		}
	}
	if !s.hitHorntailCrystal(1, actor, field.crystal.objectID) || field.crystal.state != 6 || !field.summoned {
		t.Fatal("the sixth hit did not start the encounter")
	}
	f.settle(t, func() bool {
		s.gameplay.mu.Lock()
		defer s.gameplay.mu.Unlock()
		return len(s.gameplay.rooms[key].mobs) == 9
	})
}
