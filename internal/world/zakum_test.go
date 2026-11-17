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

type zakumFixtureRepo struct {
	gameplayTestRepo
	consumed []int32
}

func (r *zakumFixtureRepo) ConsumeZakumEye(_ context.Context, _ domain.CharacterSession, id int32) error {
	r.consumed = append(r.consumed, id)
	return nil
}

func TestZakumRequiresOfferingThenArmsThenThreeBodies(t *testing.T) {
	for _, mode := range []struct {
		name                      string
		arena, lobby, exit, count int
		expedition                int
	}{
		{"Zakum", 280030000, 211042400, 211042300, 5, 2002},
		{"ChaosZakum", 280030001, 211042401, 211042301, 5, 2005},
	} {
		t.Run(mode.name, func(t *testing.T) {
			ctx := context.Background()
			f := eventTestFixture(t)
			s := f.server
			r := &zakumFixtureRepo{gameplayTestRepo: s.repo.(gameplayTestRepo)}
			s.repo = r
			root := os.Getenv("MAPLEGO_WZ_ROOT")
			dir := filepath.Join(root, "Map.wz", "Map", "Map2")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int{mode.arena, mode.lobby, mode.exit} {
				data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir></imgdir>`, id)
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			altar, _ := content.ZakumAltarByMap(mode.arena)
			for i := 0; i <= 10; i++ {
				id := altar.BaseMob + i
				data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"><int name="maxHP" value="10000"/></imgdir></imgdir>`, id)
				if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			party := domain.PartySnapshot{ID: 50, LeaderID: 1}
			for id := 1; id <= mode.count; id++ {
				party.Members = append(party.Members, domain.PartyMember{CharacterID: int64(id)})
			}
			for _, m := range party.Members[:mode.count-1] {
				f.join(t, m.CharacterID, 120, mode.lobby, party)
			}
			s.parties.addExpeditionParty(&expedition{id: 8, kind: mode.expedition, leader: 1, snapshots: map[int64]domain.PartySnapshot{}}, party)
			if reason, here, _, err := s.eventStart(ctx, mode.name, 1, f.lives[1], f.sinks[1]); err != nil || reason != "party_size" || here != mode.count-1 {
				t.Fatalf("a player short: %q count=%d err=%v", reason, here, err)
			}
			f.join(t, int64(mode.count), 120, mode.lobby, party)
			if reason, _, _, err := s.eventStart(ctx, mode.name, 1, f.lives[1], f.sinks[1]); err != nil || reason != "zakum_prerequisite" {
				t.Fatalf("missing prerequisite: reason=%q err=%v", reason, err)
			}
			for _, live := range f.lives {
				live.session.Character.QuestRecords = map[int]string{content.ZakumQuest: "2"}
				live.session.Character.Items = []domain.InventoryItem{{TemplateID: content.ZakumEye, Quantity: 1}}
			}
			if reason, _, _, err := s.eventStart(ctx, mode.name, 1, f.lives[1], f.sinks[1]); err != nil || reason != "" {
				t.Fatalf("registered raid: reason=%q err=%v", reason, err)
			}
			key := mapKey{channel: 1, mapID: mode.arena}
			room := s.gameplay.rooms[key]
			eye := domain.ItemDrop{ObjectID: 123, ChannelID: 1, MapID: mode.arena, OwnerID: 1, TemplateID: content.ZakumEye, Quantity: 1, X: 420, Y: altar.Y - 15}
			s.offerZakumEye(ctx, partySession(f.lives[1]), eye)
			if len(r.consumed) != 0 || len(room.mobs) != 0 {
				t.Fatal("a ground drop away from the altar consumed the offering")
			}
			eye.X = altar.X
			s.offerZakumEye(ctx, partySession(f.lives[1]), eye)
			eye.ObjectID++
			s.offerZakumEye(ctx, partySession(f.lives[1]), eye)
			if len(r.consumed) != 1 || len(room.mobs) != 9 || !room.mobs[room.zakum.projection].fake {
				t.Fatal("the altar did not summon exactly eight arms and one projection")
			}
			body := altar.BaseMob
			for i := 3; i <= 10; i++ {
				for _, mob := range room.mobs {
					if mob.stats.ID == body+i {
						mob.hp = 0
					}
				}
				s.zakumPartDefeated(key, 1, body+i)
				if i < 10 && room.zakum.phase != 1 {
					t.Fatal("the body unlocked before all arms were defeated")
				}
			}
			inst := s.eventOf(1)
			for i := 0; i < 3; i++ {
				want := body + i
				found := false
				for _, mob := range room.mobs {
					if mob.stats.ID == want && mob.hp > 0 && !mob.fake {
						mob.hp, found = 0, true
					}
				}
				if !found || inst.cleared {
					t.Fatalf("missing next body %d or expedition cleared prematurely", want)
				}
				s.zakumPartDefeated(key, 1, want)
			}
			if !inst.cleared || room.zakum.phase != 5 {
				t.Fatal("final body did not clear the raid")
			}
		})
	}
}
