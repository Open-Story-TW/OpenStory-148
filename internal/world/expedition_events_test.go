package world

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func hillaTestFixture(t *testing.T, levels ...int) *eventFixture {
	t.Helper()
	f := eventTestFixture(t)
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	for _, id := range []int{262010000, 262030000, 262030100, 262030200, 262030300, 262031100, 262031200, 262031300} {
		dir := filepath.Join(root, "Map.wz", "Map", "Map2")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		life := ""
		mob := map[int]int{262030100: 8870001, 262030200: 8870002, 262030300: 8870000, 262031100: 8870101, 262031200: 8870102, 262031300: 8870100}[id]
		if mob != 0 {
			count := 8
			if mob == 8870000 || mob == 8870100 {
				count = 1
			}
			var rows strings.Builder
			for i := range count {
				fmt.Fprintf(&rows, `<imgdir name="%d"><string name="type" value="m"/><string name="id" value="%d"/><int name="mobTime" value="-1"/></imgdir>`, i, mob)
			}
			life = `<imgdir name="life">` + rows.String() + `</imgdir>`
			data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"><int name="maxHP" value="100"/></imgdir></imgdir>`, mob)
			if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%d.img.xml", mob)), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"><int name="returnMap" value="262010000"/></imgdir><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir>%s</imgdir>`, id, life)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	b := f.server.parties
	party := domain.PartySnapshot{ID: 50, LeaderID: 1}
	for i := range levels {
		party.Members = append(party.Members, domain.PartyMember{CharacterID: int64(i + 1)})
	}
	for i, level := range levels {
		f.join(t, int64(i+1), level, 262030000, party)
	}
	b.addExpeditionParty(&expedition{id: 8, kind: 2010, leader: 1, snapshots: map[int64]domain.PartySnapshot{}}, party)
	return f
}

func TestHillaRequiresTheCorrectExpeditionAndFullRoster(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		levels []int
		change func(*eventFixture)
		actor  int64
		reason string
	}{
		{"solo even as GM", []int{120}, func(f *eventFixture) { f.lives[1].session.Character.GM = 1 }, 1, "party_size"},
		{"ordinary party", []int{120, 120}, func(f *eventFixture) { clear(f.server.parties.exp.byParty) }, 1, "no_expedition"},
		{"wrong expedition", []int{120, 120}, func(f *eventFixture) { f.server.parties.exp.byParty[50].kind = 2007 }, 1, "expedition_type"},
		{"only expedition leader", []int{120, 120}, nil, 2, "not_leader"},
		{"wrong lobby", []int{120, 120}, func(f *eventFixture) { f.lives[1].session.Character.MapID = 262010000 }, 1, "unavailable"},
		{"minimum reached but teammate absent", []int{120, 120, 120}, func(f *eventFixture) { f.lives[3].session.ChannelID = 2 }, 1, "roster_absent"},
		{"out of level range", []int{120, 119}, nil, 1, "level"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := hillaTestFixture(t, scenario.levels...)
			if scenario.change != nil {
				scenario.change(f)
			}
			s := f.server
			reason, _, _, err := s.eventStart(context.Background(), "Hilla", 1, f.lives[scenario.actor], f.sinks[scenario.actor])
			if err != nil || reason != scenario.reason {
				t.Fatalf("start: %q %v", reason, err)
			}
			if s.eventOf(scenario.actor) != nil {
				t.Fatal("refused attempt reserved a spot")
			}
		})
	}
}

func TestHillaStageGatesAndMinimumThroughFinalKill(t *testing.T) {
	f := hillaTestFixture(t, 120, 180)
	s, ctx := f.server, context.Background()
	if reason, here, _, err := s.eventStart(ctx, "Hilla", 1, f.lives[1], f.sinks[1]); err != nil || reason != "" || here != 2 {
		t.Fatalf("start: reason %q, here %d, err %v", reason, here, err)
	}
	inst := s.eventOf(1)
	if inst == nil || inst.expeditionID != 8 || s.eventOf(2) != inst {
		t.Fatal("native expedition was not admitted together")
	}
	usePortal := func(from, to int, clearMobs bool) {
		t.Helper()
		key := mapKey{channel: 1, mapID: from}
		s.gameplay.mu.Lock()
		for _, m := range s.gameplay.rooms[key].mobs {
			if clearMobs {
				m.hp = 0
			}
		}
		s.gameplay.mu.Unlock()
		handled, err := s.expeditionStagePortal(ctx, partySession(f.lives[1]), f.lives[1], f.sinks[1], content.Portal{Script: "hillah_next"})
		if !handled || err != nil || f.mapOf(1) != to || f.mapOf(2) != to {
			t.Fatalf("portal: handled=%t err=%v maps=%d/%d want=%d", handled, err, f.mapOf(1), f.mapOf(2), to)
		}
	}
	usePortal(262030100, 262030100, false)
	usePortal(262030100, 262030200, true)
	usePortal(262030200, 262030200, false)
	usePortal(262030200, 262030300, true)

	key := mapKey{channel: 1, mapID: 262030300}
	s.gameplay.mu.Lock()
	if !s.expeditionCombatAllowedLocked(key, 1) {
		s.gameplay.mu.Unlock()
		t.Fatal("two admitted members on boss map cannot fight")
	}
	m := s.gameplay.rooms[key].members[2]
	delete(s.gameplay.rooms[key].members, 2)
	if s.expeditionCombatAllowedLocked(key, 1) {
		s.gameplay.mu.Unlock()
		t.Fatal("attempt can solo the boss while a teammate is on another map")
	}
	s.gameplay.rooms[key].members[2] = m
	s.gameplay.mu.Unlock()
	if got := s.mapMonsterCount(1, key.mapID); got != 1 {
		t.Fatalf("boss mobs=%d", got)
	}
	f.lives[2].session.Character.HP = 0
	if err := s.reviveGameplay(ctx, 1, f.lives[2], f.sinks[2], protocol.PortalRequest{Revival: &protocol.RevivalRequest{}}); err != nil ||
		f.mapOf(2) != key.mapID || partySession(f.lives[2]).Character.HP != 50 {
		t.Fatalf("event revival: map=%d hp=%d err=%v", f.mapOf(2), partySession(f.lives[2]).Character.HP, err)
	}
	s.expeditionBossDefeated(key, 1, 8870002)
	if inst.cleared {
		t.Fatal("a guardian cannot complete the expedition")
	}
	s.expeditionBossDefeated(key, 1, 8870000)
	if !inst.cleared {
		t.Fatal("boss kill was not recorded")
	}
	s.eventExit(1, inst.definition, f.lives[2], f.sinks[2])
	if f.mapOf(2) != 262010000 || s.eventOf(2) != nil || s.eventOf(1) != inst {
		t.Fatal("post-victory exit should leave the remaining member time to loot")
	}
	s.eventExit(1, inst.definition, f.lives[1], f.sinks[1])
	f.settle(t, func() bool { return s.eventStatus(1, "Hilla")["busy"] == false })
}

func TestHillaEvictsRemovedExpeditionMembersAndEndsBelowMinimum(t *testing.T) {
	f := hillaTestFixture(t, 120, 130)
	s, b := f.server, f.server.parties
	if reason, _, _, err := s.eventStart(context.Background(), "Hilla", 1, f.lives[1], f.sinks[1]); reason != "" || err != nil {
		t.Fatalf("start: %q %v", reason, err)
	}
	b.mu.Lock()
	b.peers[2].party = domain.PartySnapshot{}
	s.eventExpeditionChangedLocked()
	b.mu.Unlock()
	if s.eventOf(2) != nil {
		t.Fatal("removed member still has admission")
	}
	f.settle(t, func() bool { return f.mapOf(1) == 262010000 && f.mapOf(2) == 262010000 })
}

func TestHillaHardChecksTheHardLevelAndBattleMaps(t *testing.T) {
	f := hillaTestFixture(t, 169, 170)
	s, ctx := f.server, context.Background()
	if reason, _, _, err := s.eventStart(ctx, "HillaHard", 1, f.lives[1], f.sinks[1]); err != nil || reason != "level" {
		t.Fatalf("under-level attempt: %q %v", reason, err)
	}
	f.lives[1].session.Character.Level = 170
	if reason, _, _, err := s.eventStart(ctx, "HillaHard", 1, f.lives[1], f.sinks[1]); err != nil || reason != "" {
		t.Fatalf("start: %q %v", reason, err)
	}
	for _, from := range []int{262031100, 262031200} {
		key := mapKey{1, from}
		s.gameplay.mu.Lock()
		for _, mob := range s.gameplay.rooms[key].mobs {
			mob.hp = 0
		}
		s.gameplay.mu.Unlock()
		handled, err := s.expeditionStagePortal(ctx, partySession(f.lives[1]), f.lives[1], f.sinks[1], content.Portal{Script: "hillah_next"})
		if !handled || err != nil || f.mapOf(2) != from+100 {
			t.Fatalf("the hard roster did not advance: %t %v %d", handled, err, f.mapOf(2))
		}
	}
	key := mapKey{1, 262031300}
	attempt := s.eventOf(1)
	s.expeditionBossDefeated(key, 1, 8870000)
	if attempt.cleared {
		t.Fatal("the normal boss template cleared hard mode")
	}
	s.expeditionBossDefeated(key, 1, 8870100)
	if !attempt.cleared {
		t.Fatal("the hard boss did not complete the attempt")
	}
}
