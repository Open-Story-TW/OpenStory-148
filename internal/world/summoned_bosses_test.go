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

func TestExpeditionChallengeNPCStartsTheBossExactlyOnce(t *testing.T) {
	for arena, mode := range summonedBosses {
		t.Run(mode.event, func(t *testing.T) {
			f := eventTestFixture(t)
			e, _ := content.EventByName(mode.event)
			root := os.Getenv("MAPLEGO_WZ_ROOT")
			dir := filepath.Join(root, "Map.wz", "Map", "Map2")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, mapID := range []int{arena, e.LobbyMap, e.ExitMap} {
				life := ""
				if mapID == arena && mode.event == "Arkarium" {
					life = `<imgdir name="life"><imgdir name="0"><string name="type" value="m"/><string name="id" value="9300304"/><int name="mobTime" value="-1"/></imgdir></imgdir>`
				}
				data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir>%s</imgdir>`, mapID, life)
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.img.xml", mapID)), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []int{mode.intro, mode.body, 9300304} {
				data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"><int name="maxHP" value="100000"/></imgdir></imgdir>`, id)
				if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := f.server
			p := domain.PartySnapshot{ID: 50, LeaderID: 1}
			for id := int64(1); id <= 6; id++ {
				p.Members = append(p.Members, domain.PartyMember{CharacterID: id})
			}
			for _, m := range p.Members[:len(p.Members)-1] {
				f.join(t, m.CharacterID, 150, e.LobbyMap, p)
			}
			s.parties.addExpeditionParty(&expedition{id: 8, kind: e.ExpeditionType, leader: 1, snapshots: map[int64]domain.PartySnapshot{}}, p)
			if reason, here, _, err := s.eventStart(context.Background(), mode.event, 1, f.lives[1], f.sinks[1]); err != nil || reason != "party_size" || here != 5 {
				t.Fatalf("a player short: %q count=%d err=%v", reason, here, err)
			}
			f.join(t, 6, 150, e.LobbyMap, p)
			if reason, _, _, err := s.eventStart(context.Background(), mode.event, 1, f.lives[1], f.sinks[1]); err != nil || reason != "" {
				t.Fatalf("start: %q %v", reason, err)
			}
			inst := s.eventOf(1)
			key := mapKey{channel: 1, mapID: arena}
			room := s.gameplay.rooms[key]
			var npcOID int32
			for oid, life := range room.npcs {
				if life.TemplateID == mode.npc {
					npcOID = oid
				}
			}
			if npcOID == 0 {
				t.Fatal("challenge NPC missing")
			}
			session := partySession(f.lives[1])
			if mode.event == "Arkarium" {
				if s.summonExpeditionBoss(inst, session, mode.npc, npcOID) {
					t.Fatal("the live clone did not block the challenge")
				}
				for _, m := range room.mobs {
					m.hp = 0
				}
			}
			if !s.summonExpeditionBoss(inst, session, mode.npc, npcOID) || s.summonExpeditionBoss(inst, session, mode.npc, npcOID) {
				t.Fatal("the challenge must start once")
			}
			if _, exists := room.npcs[npcOID]; exists {
				t.Fatal("the challenge NPC remained available")
			}
			f.settle(t, func() bool {
				s.gameplay.mu.Lock()
				defer s.gameplay.mu.Unlock()
				for _, m := range room.mobs {
					if m.life.TemplateID == mode.body && m.hp > 0 && !m.fake {
						m.hp = 0
						return true
					}
				}
				return false
			})
			s.finishSummonedBoss(mode, key, 1, mode.body)
			if !inst.cleared || len(room.npcs) != len(mode.victoryNPCs) {
				t.Fatalf("clear is missing: cleared=%t npcs=%v", inst.cleared, room.npcs)
			}
		})
	}
}
