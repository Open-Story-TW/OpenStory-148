package world

import (
	"context"
	"encoding/binary"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
	"time"
)

type questDropWorldRepo struct {
	domain.Repository
	drops []domain.ItemDrop
	lists int
}

func questDropRaceFixture() (*Server, domain.CharacterSession, domain.ItemDrop, *memSink) {
	a := domain.CharacterSession{CharacterID: 1, SessionID: "quest-drop-owner", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, Revision: 10, Quests: []domain.QuestState{{ID: 1035, State: 1}}}}
	d := domain.ItemDrop{ObjectID: 1000000222, WorldID: 0, ChannelID: 1, MapID: 40000, OwnerID: 1, InvType: 4, TemplateID: 4031802, Quantity: 1, QuestID: 1035, QuestDropType: 0}
	sink := &memSink{}
	s := &Server{drops: newDropSpace()}
	s.drops.Track(1, 40000, 1, sink, a)
	return s, a, d, sink
}

func TestDropSessionCloneOwnsQuestCounterMapsAndOrder(t *testing.T) {
	s := domain.CharacterSession{Character: domain.Character{Items: []domain.InventoryItem{{TemplateID: 4031802, Quantity: 1}}, Quests: []domain.QuestState{{ID: 1035, State: 1, MobKills: map[int]int{9300018: 1}, MobOrder: []int{9300018}}}}}
	copy := cloneDropSession(s)
	s.Character.Items[0].Quantity = 99
	s.Character.Quests[0].State = 0
	s.Character.Quests[0].MobKills[9300018] = 99
	s.Character.Quests[0].MobOrder[0] = 123
	if copy.Character.Items[0].Quantity != 1 || copy.Character.Quests[0].State != 1 || copy.Character.Quests[0].MobKills[9300018] != 1 || copy.Character.Quests[0].MobOrder[0] != 9300018 {
		t.Fatal("drop snapshot retains mutable character data")
	}
}

func TestQuestDropDelayedBroadcastCannotResurrectInvisibleNeedOrForfeit(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	for _, sameRevision := range []bool{false, true} {
		s, old, d, sink := questDropRaceFixture()
		current := cloneDropSession(old)
		if sameRevision {
			current.Character.Quests = []domain.QuestState{{ID: 1035, State: 0}}
		} else {
			current.Character.Revision++
			current.Character.Items = []domain.InventoryItem{{InvType: 4, TemplateID: 4031802, Quantity: 1}}
		}
		s.drops.UpdateSession(current)
		// A new authoritative decision happens before a previously captured
		// broadcast callback runs. Even never-visible false must be remembered.
		if err := s.reconcileQuestDrop(current, sink, d); err != nil {
			t.Fatal(err)
		}
		if err := s.reconcileQuestDrop(old, sink, d); err != nil {
			t.Fatal(err)
		}
		if len(sink.pkts) != 0 {
			t.Fatal("late callback revived ineligible object")
		}
		decision, exists := s.drops.questViews[mapKey{1, 40000}][1][d.ObjectID]
		if !exists || decision.visible || decision.revision != current.Character.Revision {
			t.Fatal("invisible watermark was discarded", decision)
		}
		// Callback storage cannot borrow a live slice that gets modified later.
		current.Character.Quests[0].State = 1
		if sameRevision && s.drops.questTracks[mapKey{1, 40000}][1].session.Character.Quests[0].State != 0 {
			t.Fatal("track borrowed mutable quest slice")
		}
	}
}

func TestQuestDropStaleMapSinkGenerationAndLeaseCannotPublish(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	for _, replace := range []string{"map", "sink", "generation", "session", "world"} {
		s, old, d, sink := questDropRaceFixture()
		next := cloneDropSession(old)
		replacement := &memSink{}
		s.drops.Untrack(1, 40000, 1)
		switch replace {
		case "map":
			next.Character.MapID = 50000
		case "generation":
			next.Generation++
		case "session":
			next.SessionID = "new-owner-session"
		case "world":
			next.Lease.WorldID = 1
		}
		currentSink := sink
		if replace == "sink" {
			currentSink = replacement
		}
		s.drops.Track(next.ChannelID, next.Character.MapID, 1, currentSink, next)
		if err := s.reconcileQuestDrop(old, sink, d); err != nil {
			t.Fatal(err)
		}
		if len(sink.pkts) != 0 || len(replacement.pkts) != 0 {
			t.Fatal("stale registration published after", replace)
		}
		if replace == "map" && s.drops.questViews[mapKey{1, 40000}] != nil {
			t.Fatal("old room was recreated after warp")
		}
	}
}

func TestQuestDropExpiredProtectionReentrySendsFFAWithoutChangingAudit(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	for _, kind := range []int{0, 1} {
		s, a, d, sink := questDropRaceFixture()
		d.QuestDropType = kind
		d.QuestOwnerUntil = time.Now().Add(-time.Second).UnixMilli()
		if err := s.reconcileQuestDrop(a, sink, d); err != nil {
			t.Fatal(err)
		}
		if len(sink.pkts) != 1 || binary.LittleEndian.Uint16(sink.pkts[0]) != protocol.SendDropItem || sink.pkts[0][16] != 2 {
			t.Fatal("expired drop was resent with protected owner type")
		}
		if d.QuestDropType != kind {
			t.Fatal("original drop audit metadata changed")
		}
	}
}

func (r *questDropWorldRepo) ListItemDrops(context.Context, int, int, int) ([]domain.ItemDrop, error) {
	r.lists++
	return r.drops, nil
}
func (r *questDropWorldRepo) PickupItem(context.Context, domain.ItemPickupRequest) (domain.InventoryResult, error) {
	return domain.InventoryResult{}, domain.ErrForbidden
}
func (r *questDropWorldRepo) MoveInventory(context.Context, domain.InventoryMoveRequest) (domain.InventoryResult, error) {
	return domain.InventoryResult{}, domain.ErrForbidden
}

func TestQuestDropsVisibilityReconcilesActiveNeedAndNoOrdinaryRespawn(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	d := domain.ItemDrop{ObjectID: 1000000100, WorldID: 0, ChannelID: 1, MapID: 40000, OwnerID: 1, InvType: 4, TemplateID: 4031802, Quantity: 1, QuestID: 1035, QuestOwnerUntil: time.Now().Add(30 * time.Second).UnixMilli()}
	ordinary := domain.ItemDrop{ObjectID: 1000000101, WorldID: 0, ChannelID: 1, MapID: 40000, OwnerID: 1, InvType: 4, TemplateID: 4000019, Quantity: 1}
	repo := &questDropWorldRepo{drops: []domain.ItemDrop{d, ordinary}}
	a := domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000, Quests: []domain.QuestState{{ID: 1035, State: 1}}}}
	b := domain.CharacterSession{CharacterID: 2, ChannelID: 1, Character: domain.Character{ID: 2, MapID: 40000}}
	sinks := []*memSink{{}, {}}
	s := &Server{repo: repo, drops: newDropSpace(), online: map[int64]domain.CharacterSession{1: a, 2: b}}
	s.drops.Track(1, 40000, 1, sinks[0], a)
	s.drops.Track(1, 40000, 2, sinks[1], b)
	s.broadcastCombatDrop(d)
	if len(sinks[0].pkts) != 1 || len(sinks[1].pkts) != 0 {
		t.Fatal("quest drop disclosed to inactive peer")
	}
	s.broadcastCombatDrop(d)
	if len(sinks[0].pkts) != 1 {
		t.Fatal("duplicate quest object spawned")
	}
	b.Character.Quests = []domain.QuestState{{ID: 1035, State: 1}}
	s.online[2] = b
	s.drops.UpdateSession(b)
	if err := s.refreshQuestDrops(context.Background(), b, sinks[1]); err != nil {
		t.Fatal(err)
	}
	if len(sinks[1].pkts) != 1 || sinks[1].opcodes()[0] != protocol.SendDropItem {
		t.Fatal("newly eligible quest peer could not see existing drop")
	}
	a.Character.Items = []domain.InventoryItem{{TemplateID: 4031802, InvType: 4, Quantity: 1}}
	s.online[1] = a
	s.drops.UpdateSession(a)
	if err := s.refreshQuestDrops(context.Background(), a, sinks[0]); err != nil {
		t.Fatal(err)
	}
	if len(sinks[0].pkts) != 2 || sinks[0].opcodes()[1] != protocol.SendRemoveItem {
		t.Fatal("collection requirement did not remove remaining object")
	}
	b.Character.Quests[0].State = 0
	s.drops.UpdateSession(b)
	if err := s.refreshQuestDrops(context.Background(), b, sinks[1]); err != nil {
		t.Fatal(err)
	}
	if len(sinks[1].pkts) != 2 || sinks[1].opcodes()[1] != protocol.SendRemoveItem {
		t.Fatal("forfeit did not hide quest object")
	}
	s.broadcastCombatDrop(ordinary)
	if len(sinks[0].pkts) != 3 || len(sinks[1].pkts) != 3 {
		t.Fatal("ordinary public drop policy changed")
	}
	s.removeVisibleDrop(d, protocol.RemoveItemExpired(d.ObjectID))
	if len(sinks[0].pkts) != 3 || len(sinks[1].pkts) != 3 {
		t.Fatal("expiry sent to characters without a visible quest object")
	}
}

func TestQuestDropEntryFiltersAndClearsViewsOnMapLeave(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	d := domain.ItemDrop{ObjectID: 1000000110, ChannelID: 1, MapID: 40000, OwnerID: 1, InvType: 4, TemplateID: 4031802, Quantity: 1, QuestID: 1035}
	repo := &questDropWorldRepo{drops: []domain.ItemDrop{d}}
	s := &Server{repo: repo, drops: newDropSpace()}
	sink := &memSink{}
	a := domain.CharacterSession{CharacterID: 1, ChannelID: 1, Character: domain.Character{ID: 1, MapID: 40000}}
	s.drops.Track(1, 40000, 1, sink, a)
	if err := s.sendInventoryDrops(context.Background(), a, sink); err != nil || len(sink.pkts) != 0 {
		t.Fatal("initial field snapshot leaked inactive quest drop")
	}
	a.Character.Quests = []domain.QuestState{{ID: 1035, State: 1}}
	s.drops.UpdateSession(a)
	if err := s.sendInventoryDrops(context.Background(), a, sink); err != nil || len(sink.pkts) != 1 {
		t.Fatal("initial eligible drop missing")
	}
	s.drops.Untrack(1, 40000, 1)
	s.drops.Track(1, 40000, 1, sink, a)
	if err := s.sendInventoryDrops(context.Background(), a, sink); err != nil || len(sink.pkts) != 2 {
		t.Fatal("old map visibility suppressed reentry object")
	}
}
