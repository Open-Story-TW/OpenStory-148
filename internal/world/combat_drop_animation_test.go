package world

import (
	"context"
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type animationLootRepo struct {
	questDropWorldRepo
	materialized bool
}

func (r *animationLootRepo) PendingCombatLoot(context.Context, domain.WorldLease, int) ([]string, error) {
	return nil, nil
}

func (r *animationLootRepo) MaterializeCombatLoot(context.Context, domain.WorldLease, string) ([]domain.ItemDrop, error) {
	if r.materialized {
		return nil, nil
	}
	r.materialized = true
	return r.drops, nil
}

func TestCombatLootAnimatesInitialMaterializationButNotLateEntry(t *testing.T) {
	drops := []domain.ItemDrop{
		{ObjectID: 1000000100, WorldID: 0, ChannelID: 1, MapID: 40000, OwnerID: 1, InvType: 2, TemplateID: 2000000, Quantity: 1, X: -192, Y: 210},
		{ObjectID: 1000000101, WorldID: 0, ChannelID: 1, MapID: 40000, OwnerID: 1, Mesos: 123, Quantity: 1, X: -192, Y: 210},
	}
	repo := &animationLootRepo{questDropWorldRepo: questDropWorldRepo{drops: drops}}
	s := &Server{repo: repo, drops: newDropSpace()}
	owner := domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, X: 88, Y: 99}}
	peer := owner
	peer.CharacterID, peer.Character.ID = 2, 2
	peer.Character.X, peer.Character.Y = 300, 400
	a, b := &memSink{}, &memSink{}
	s.drops.Track(1, 40000, 1, a, owner)
	s.drops.Track(1, 40000, 2, b, peer)
	if err := s.materializeCombatLoot(context.Background(), repo, "committed-loot-job"); err != nil {
		t.Fatal(err)
	}
	for _, sink := range []*memSink{a, b} {
		if len(sink.pkts) != 2 {
			t.Fatal("missing initial materialization", sink.pkts)
		}
		for i, packet := range sink.pkts {
			if binary.LittleEndian.Uint16(packet) != protocol.SendDropItem || packet[2] != 1 || int16(binary.LittleEndian.Uint16(packet[25:27])) != -192 || int16(binary.LittleEndian.Uint16(packet[27:29])) != 210 || binary.LittleEndian.Uint16(packet[29:31]) != 0 {
				t.Fatalf("source/delay taken from current peer instead of committed kill: %x", packet)
			}
			if binary.LittleEndian.Uint32(packet[3:7]) != uint32(drops[i].ObjectID) {
				t.Fatal("drop identity changed")
			}
		}
	}
	if err := s.materializeCombatLoot(context.Background(), repo, "committed-loot-job"); err != nil || len(a.pkts) != 2 || len(b.pkts) != 2 {
		t.Fatal("already materialized job replayed animation", err)
	}
	late := &memSink{}
	if err := s.sendInventoryDrops(context.Background(), owner, late); err != nil || len(late.pkts) != 2 {
		t.Fatal("late entry", late.pkts, err)
	}
	for _, packet := range late.pkts {
		if packet[2] != 2 {
			t.Fatal("late entry replayed throw", packet)
		}
	}
}

func TestQuestCombatDropInitialAnimationKeepsVisibilityAndRefreshStationary(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	s, owner, drop, sink := questDropRaceFixture()
	s.online = map[int64]domain.CharacterSession{owner.CharacterID: owner}
	s.repo = &questDropWorldRepo{drops: []domain.ItemDrop{drop}}
	s.broadcastCombatDrop(drop)
	s.broadcastCombatDrop(drop)
	if len(sink.pkts) != 1 || sink.pkts[0][2] != 1 {
		t.Fatal("initial quest drop missing or repeated animation", sink.pkts)
	}
	s.drops.Untrack(1, 40000, owner.CharacterID)
	s.drops.Track(1, 40000, owner.CharacterID, sink, owner)
	if err := s.refreshQuestDrops(context.Background(), owner, sink); err != nil || len(sink.pkts) != 2 || sink.pkts[1][2] != 2 {
		t.Fatal("quest refresh must retain mode2", err, sink.pkts)
	}
	owner.Character.Quests[0].State = 0
	s.drops.UpdateSession(owner)
	if err := s.refreshQuestDrops(context.Background(), owner, sink); err != nil {
		t.Fatal(err)
	}
	before := len(sink.pkts)
	s.broadcastCombatDrop(drop)
	if len(sink.pkts) != before {
		t.Fatal("animation bypassed quest eligibility")
	}
}
