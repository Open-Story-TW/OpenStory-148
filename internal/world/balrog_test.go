package world

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func balrogTestFixture(t *testing.T, size int) *eventFixture {
	t.Helper()
	f := eventTestFixture(t)
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	path := filepath.Join(root, "Map.wz", "Map", "Map1")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{105100100, balrogArena, balrogRewardMap} {
		data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"/><imgdir name="portal"><imgdir name="0"><string name="pn" value="sp"/><int name="pt" value="0"/></imgdir></imgdir></imgdir>`, id)
		if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for id := 8830007; id <= 8830009; id++ {
		data := fmt.Sprintf(`<imgdir name="%d.img"><imgdir name="info"><int name="maxHP" value="2400000"/></imgdir></imgdir>`, id)
		if err := os.WriteFile(filepath.Join(root, "Mob.wz", fmt.Sprintf("%d.img.xml", id)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	party := domain.PartySnapshot{ID: 50, LeaderID: 1}
	for id := 1; id <= size; id++ {
		party.Members = append(party.Members, domain.PartyMember{CharacterID: int64(id)})
	}
	for _, m := range party.Members {
		f.join(t, m.CharacterID, 45, 105100100, party)
	}
	f.server.parties.addExpeditionParty(&expedition{id: 8, kind: 2000, leader: 1, snapshots: map[int64]domain.PartySnapshot{}}, party)
	return f
}

func TestBalrogOriginalRosterMinimum(t *testing.T) {
	f := balrogTestFixture(t, 1)
	reason, _, _, err := f.server.eventStart(context.Background(), "BalrogEasy", 1, f.lives[1], f.sinks[1])
	if err != nil || reason != "" || f.mapOf(1) != balrogArena {
		t.Fatalf("expedition member: reason=%q map=%d err=%v", reason, f.mapOf(1), err)
	}
}

func TestBalrogOpeningWithThreeMembers(t *testing.T) {
	f := balrogTestFixture(t, 3)
	s := f.server
	reason, _, _, err := s.eventStart(context.Background(), "BalrogEasy", 1, f.lives[1], f.sinks[1])
	if err != nil || reason != "" || f.mapOf(3) != balrogArena {
		t.Fatalf("three members: reason=%q map=%d err=%v", reason, f.mapOf(3), err)
	}
	inst := s.eventOf(1)
	key := mapKey{channel: 1, mapID: balrogArena}
	s.gameplay.mu.Lock()
	mobs := s.gameplay.rooms[key].mobs
	for _, m := range mobs {
		if !m.prelude || m.lethalHP() != m.hp-1 {
			t.Fatal("opening part can be defeated")
		}
		m.hp -= balrogWarmupDamage
	}
	old := make(map[int32]bool)
	for oid := range mobs {
		old[oid] = true
	}
	s.gameplay.mu.Unlock()
	s.tickEvents(inst.preludeUntil.Add(-time.Millisecond))
	if inst.preludeUntil.IsZero() {
		t.Fatal("damage skipped the opening timer")
	}
	s.tickEvents(inst.preludeUntil)
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	if len(mobs) != 3 || !inst.preludeUntil.IsZero() {
		t.Fatalf("opening not replaced: mobs=%d", len(mobs))
	}
	for oid, m := range mobs {
		if old[oid] || m.prelude || m.hp != 2400000 {
			t.Fatalf("part still uses opening HP/state: %d %+v", oid, m)
		}
	}
}

func TestBalrogWarmupFailureReturnsEveryone(t *testing.T) {
	f := balrogTestFixture(t, 3)
	s := f.server
	if reason, _, _, err := s.eventStart(context.Background(), "BalrogEasy", 1, f.lives[1], f.sinks[1]); err != nil || reason != "" {
		t.Fatalf("start: reason=%q err=%v", reason, err)
	}
	s.tickEvents(s.eventOf(1).preludeUntil)
	f.settle(t, func() bool {
		return s.eventOf(1) == nil && f.mapOf(1) == 105100100 && f.mapOf(2) == 105100100 && f.mapOf(3) == 105100100
	})
}

type balrogRewardTestRepo struct {
	gameplayTestRepo
	exchanges int
	request   domain.ScriptExchange
}

func (*balrogRewardTestRepo) ReadScriptLogs(context.Context, domain.CharacterSession, []domain.ScriptLogKey) ([]domain.ScriptLogValue, error) {
	return nil, nil
}

func (r *balrogRewardTestRepo) ExecuteScriptExchange(_ context.Context, q domain.ScriptExchange) (domain.ScriptExchangeResult, error) {
	r.exchanges++
	r.request = q
	c := q.Session.Character
	c.MapID, c.X, c.Y, c.SpawnPoint = q.Travel.MapID, q.Travel.X, q.Travel.Y, q.Travel.SpawnPoint
	c.EXP += q.EXP
	c.Revision++
	return domain.ScriptExchangeResult{InventoryResult: domain.InventoryResult{Character: c}}, nil
}

func TestBalrogRewardPublishesTheCommittedReturnOnce(t *testing.T) {
	f := balrogTestFixture(t, 3)
	s := f.server
	ctx := context.Background()
	r := &balrogRewardTestRepo{gameplayTestRepo: s.repo.(gameplayTestRepo)}
	s.repo = r
	if reason, _, _, err := s.eventStart(ctx, "BalrogEasy", 1, f.lives[1], f.sinks[1]); reason != "" || err != nil {
		t.Fatalf("start: reason=%q err=%v", reason, err)
	}
	inst := s.eventOf(1)
	// A valid member without a completed round cannot claim the prize
	// even if another server action has placed it on the bonus map.
	bonus, err := s.gameplay.catalog.Map(balrogRewardMap)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := bonus.Spawn()
	if err = s.warpEventMember(ctx, inst, balrogArena, eventOccupant{f.lives[1], f.sinks[1]}, bonus.ID, entry); err != nil {
		t.Fatal(err)
	}
	leaving := partySession(f.lives[1])
	if err := s.balrogRewardPortal(ctx, leaving, f.lives[1], f.sinks[1]); err != nil || r.exchanges != 0 {
		t.Fatalf("uncleared reward: exchanges=%d err=%v", r.exchanges, err)
	}
	s.events.mu.Lock()
	inst.cleared = true
	s.events.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- s.balrogRewardPortal(ctx, leaving, f.lives[1], f.sinks[1]) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reward publication blocked the field return")
	}
	if f.mapOf(1) != 105100100 || s.eventOf(1) != nil || s.eventOf(2) != inst || r.exchanges != 1 ||
		len(r.request.Items) != 1 || r.request.Items[0].TemplateID != balrogSeal || r.request.EXP != domain.RateExperience(balrogRewardEXP) {
		t.Fatalf("return/reward: map=%d exchanges=%d items=%v exp=%d", f.mapOf(1), r.exchanges, r.request.Items, r.request.EXP)
	}
	if err := s.balrogRewardPortal(ctx, leaving, f.lives[1], f.sinks[1]); err != nil || r.exchanges != 1 {
		t.Fatalf("replayed field request: exchanges=%d err=%v", r.exchanges, err)
	}
}
