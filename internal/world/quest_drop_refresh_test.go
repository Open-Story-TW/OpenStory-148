package world

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type refreshDropRepository struct {
	domain.Repository
	mu                     sync.Mutex
	calls, active, maximum int
	list                   func(context.Context, int) ([]domain.ItemDrop, error)
}

func (r *refreshDropRepository) ListItemDrops(ctx context.Context, _ int, _ int, _ int) ([]domain.ItemDrop, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.active++
	r.maximum = max(r.maximum, r.active)
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock() }()
	return r.list(ctx, n)
}
func (r *refreshDropRepository) PickupItem(context.Context, domain.ItemPickupRequest) (domain.InventoryResult, error) {
	return domain.InventoryResult{}, errors.New("unexpected pickup")
}
func (r *refreshDropRepository) MoveInventory(context.Context, domain.InventoryMoveRequest) (domain.InventoryResult, error) {
	return domain.InventoryResult{}, errors.New("unexpected move")
}
func waitQuestRefreshIdle(t *testing.T, d *dropSpace) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		d.mu.Lock()
		idle := len(d.questRefresh) == 0
		d.mu.Unlock()
		if idle {
			return
		}
		select {
		case <-deadline:
			t.Fatal("quest refresh worker did not finish")
			return
		case <-time.After(time.Millisecond):
		}
	}
}

func TestCentralInventoryPublicationHidesAndShowsNeededDropsWithoutSlotMoveSQL(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	s, a, d, sink := questDropRaceFixture()
	live := &liveCharacter{session: a}
	s.online = map[int64]domain.CharacterSession{1: a}
	repo := &refreshDropRepository{list: func(context.Context, int) ([]domain.ItemDrop, error) {
		// The worker must not perform SQL while holding world/drop/map locks.
		if !s.drops.mu.TryLock() {
			t.Error("drop mutex held during SQL")
		} else {
			s.drops.mu.Unlock()
		}
		if !s.mu.TryLock() {
			t.Error("online mutex held during SQL")
		} else {
			s.mu.Unlock()
		}
		return []domain.ItemDrop{d}, nil
	}}
	s.repo = repo
	if err := s.reconcileQuestDrop(a, sink, d); err != nil {
		t.Fatal(err)
	}
	value := cloneDropSession(a).Character
	value.Revision++
	value.Items = []domain.InventoryItem{{ID: 1, InvType: 4, Slot: 1, TemplateID: 4031802, Quantity: 1}}
	s.applyInventoryResult(live, value)
	waitQuestRefreshIdle(t, s.drops)
	ops := sink.opcodes()
	if len(ops) != 2 || ops[1] != protocol.SendRemoveItem {
		t.Fatal("withdraw/script grant did not hide already-visible drop", ops)
	}
	value.Revision++
	value.Items[0].Slot = 4
	s.applyInventoryResult(live, value)
	waitQuestRefreshIdle(t, s.drops)
	repo.mu.Lock()
	calls := repo.calls
	repo.mu.Unlock()
	if calls != 1 {
		t.Fatal("slot move queried ground drops", calls)
	}
	// Consuming/depositing the required item makes the same ground object
	// visible again through the common result hook, without caller-specific code.
	value.Revision++
	value.Items = nil
	s.applyInventoryResult(live, value)
	waitQuestRefreshIdle(t, s.drops)
	ops = sink.opcodes()
	if len(ops) != 3 || ops[2] != protocol.SendDropItem {
		t.Fatal("deposit/consume did not redisplay needed drop", ops)
	}
	// A red potion quantity is irrelevant to quest1035's collection requirement.
	value.Revision++
	value.Items = []domain.InventoryItem{{ID: 2, InvType: 2, Slot: 1, TemplateID: 2000000, Quantity: 1}}
	s.applyInventoryResult(live, value)
	waitQuestRefreshIdle(t, s.drops)
	repo.mu.Lock()
	calls = repo.calls
	repo.mu.Unlock()
	if calls != 2 {
		t.Fatal("unrelated template queried drops", calls)
	}
}

func TestQuestRefreshReplacementCancelsAndSerializesOldWorkerBeforeFreshQuery(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	s, a, d, sink := questDropRaceFixture()
	s.online = map[int64]domain.CharacterSession{1: a}
	live := &liveCharacter{session: a}
	firstEntered, firstRelease, secondEntered, secondRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	repo := &refreshDropRepository{list: func(ctx context.Context, n int) ([]domain.ItemDrop, error) {
		if n == 1 {
			close(firstEntered)
			<-firstRelease
		} else if n == 2 {
			close(secondEntered)
			<-secondRelease
		}
		return []domain.ItemDrop{d}, nil
	}}
	s.repo = repo
	filled := cloneDropSession(a).Character
	filled.Revision++
	filled.Items = []domain.InventoryItem{{ID: 1, InvType: 4, Slot: 1, TemplateID: 4031802, Quantity: 1}}
	s.applyInventoryResult(live, filled)
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("initial worker not started")
	}
	// Reuse the same sink/session/revision on purpose: the monotonic entry token
	// must reject the old DB result independently of generation/revision guards.
	next := live.session
	s.drops.Untrack(1, 40000, 1, sink)
	s.drops.Track(1, 40000, 1, sink, next)
	replacement := &liveCharacter{session: next}
	empty := filled
	empty.Items = nil
	s.applyInventoryResult(replacement, empty)
	repo.mu.Lock()
	calls, maximum := repo.calls, repo.maximum
	repo.mu.Unlock()
	if calls != 1 || maximum != 1 {
		t.Fatal("replacement started a parallel stale worker")
	}
	close(firstRelease)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("fresh pending query was lost")
	}
	if len(sink.opcodes()) != 0 {
		t.Fatal("old track's result reached the replacement sink")
	}
	close(secondRelease)
	waitQuestRefreshIdle(t, s.drops)
	if ops := sink.opcodes(); len(ops) != 1 || ops[0] != protocol.SendDropItem {
		t.Fatal("fresh track did not receive fresh query result", ops)
	}
	repo.mu.Lock()
	maximum = repo.maximum
	repo.mu.Unlock()
	if maximum != 1 {
		t.Fatal("more than one worker per character")
	}
}
