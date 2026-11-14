package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"maple-go-server/internal/domain"
)

func TestBuddyInviteAcceptOfflineReloadAndDirectedDelete(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	a, b := actors[0], actors[1]
	q := domain.BuddyRequest{Session: a, Action: 1, Name: b.Character.Name, Group: "朋友"}
	got, err := s.MutateBuddy(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Invitation || len(got.List.Entries) != 1 || got.List.Entries[0].Pending || len(got.TargetList.Entries) != 1 || !got.TargetList.Entries[0].Pending || got.List.Capacity != 232 {
		t.Fatalf("invite state %+v", got)
	}
	if _, err = s.MutateBuddy(ctx, q); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate invite: %v", err)
	}
	for _, actor := range actors {
		list, err := s.ReadBuddies(ctx, actor)
		if err != nil || len(list.Entries) != 1 || list.Entries[0].Channel != 0 {
			t.Fatalf("reloaded offline list: %+v %v", list, err)
		}
	}
	got, err = s.MutateBuddy(ctx, domain.BuddyRequest{Session: b, Action: 2, TargetID: a.CharacterID})
	if err != nil || got.List.Entries[0].Pending || got.TargetList.Entries[0].Pending {
		t.Fatalf("accept %+v %v", got, err)
	}
	if _, err = s.MutateBuddy(ctx, domain.BuddyRequest{Session: b, Action: 2, TargetID: a.CharacterID}); err != nil {
		t.Fatalf("replayed accept: %v", err)
	}
	q.Group = "公會朋友"
	got, err = s.MutateBuddy(ctx, q)
	if err != nil || got.Invitation || got.List.Entries[0].Group != q.Group {
		t.Fatalf("group change %+v %v", got, err)
	}
	got, err = s.MutateBuddy(ctx, domain.BuddyRequest{Session: b, Action: 3, TargetID: a.CharacterID})
	if err != nil || len(got.List.Entries) != 0 || len(got.TargetList.Entries) != 1 {
		t.Fatalf("directed delete %+v %v", got, err)
	}
	if _, err = s.MutateBuddy(ctx, domain.BuddyRequest{Session: b, Action: 2, TargetID: a.CharacterID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("forged accept after deletion: %v", err)
	}
	got, err = s.MutateBuddy(ctx, domain.BuddyRequest{Session: b, Action: 1, Name: a.Character.Name, Group: "Rejoined"})
	if err != nil || got.Invitation || got.List.Entries[0].Pending || got.TargetList.Entries[0].Pending {
		t.Fatalf("mutual re-add %+v %v", got, err)
	}
}

func TestBuddyConcurrentInviteCapacityFencingAndRollback(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	a, b := actors[0], actors[1]
	var successes atomic.Int32
	var wg sync.WaitGroup
	q := domain.BuddyRequest{Session: a, Action: 1, Name: b.Character.Name, Group: "One"}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.MutateBuddy(ctx, q); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, domain.ErrAlreadyExists) {
				t.Errorf("parallel add: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("%d invitations created", successes.Load())
	}
	if _, err := s.MutateBuddy(ctx, domain.BuddyRequest{Session: a, Action: 3, TargetID: b.CharacterID}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ReadBuddies(ctx, b)
	if err != nil || len(list.Entries) != 0 {
		t.Fatalf("withdraw leaves pending %+v %v", list, err)
	}
	bad := q
	bad.Session.Generation++
	if _, err = s.MutateBuddy(ctx, bad); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("generation: %v", err)
	}
	bad = q
	bad.Name = a.Character.Name
	if _, err = s.MutateBuddy(ctx, bad); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("self: %v", err)
	}
	bad = q
	bad.Name = "NoSuchHero"
	if _, err = s.MutateBuddy(ctx, bad); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	name := fmt.Sprintf("buddy_fail_%d", b.CharacterID)
	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test buddy reverse write failure'; END $$; CREATE TRIGGER %s BEFORE INSERT ON game_buddies FOR EACH ROW WHEN (NEW.character_id=%d) EXECUTE FUNCTION %s()`, name, name, b.CharacterID, name))
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_, _ = s.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON game_buddies; DROP FUNCTION IF EXISTS %s()", name, name))
	}
	t.Cleanup(cleanup)
	if _, err = s.MutateBuddy(ctx, q); err == nil {
		t.Fatal("partial relationship committed")
	}
	list, err = s.ReadBuddies(ctx, a)
	if err != nil || len(list.Entries) != 0 {
		t.Fatalf("sender write failed to rollback %+v %v", list, err)
	}
	cleanup()
	// A capacity occupied by one real persisted entry rejects another pair.
	if _, err = s.db.ExecContext(ctx, `UPDATE game_buddy_profiles SET capacity=1 WHERE character_id=$1`, b.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateBuddy(ctx, q); err != nil {
		t.Fatal(err)
	}
	list, err = s.ReadBuddies(ctx, b)
	if err != nil || list.Capacity != 1 || len(list.Entries) != 1 {
		t.Fatalf("capacity %+v %v", list, err)
	}
	// Cross-world targets cannot be discovered or accepted.
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET world_id=123 WHERE id=$1`, b.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateBuddy(ctx, q); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-world name: %v", err)
	}
	if _, err = s.MutateBuddy(ctx, domain.BuddyRequest{Session: a, Action: 2, TargetID: b.CharacterID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-world ID: %v", err)
	}
}

func TestBuddyCompetingInvitesCannotOverfillOfflineTarget(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	name := fmt.Sprintf("BD%d", actors[0].CharacterID)
	var thirdID int64
	// This is an isolated persistence fixture, not a gameplay acceptance action.
	err := s.db.QueryRowContext(ctx, `INSERT INTO game_characters(account_id,world_id,name,name_key,operation_id,gender,skin,face,hair)
	VALUES($1,124,$2,lower($2),$3,0,0,20100,30000) RETURNING id`, actors[0].AccountID, name, "buddy-fixture-"+name).Scan(&thirdID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO game_buddy_profiles(character_id,capacity) VALUES($1,1)`, thirdID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var wins atomic.Int32
	var winner atomic.Int32
	for i, actor := range actors {
		wg.Add(1)
		go func(i int, actor domain.CharacterSession) {
			defer wg.Done()
			_, err := s.MutateBuddy(ctx, domain.BuddyRequest{Session: actor, Action: 1, Name: name, Group: "Friends"})
			if err == nil {
				wins.Add(1)
				winner.Store(int32(i))
			} else if !errors.Is(err, domain.ErrBuddyTargetFull) {
				t.Errorf("competing invite: %v", err)
			}
		}(i, actor)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("offline capacity allowed %d winners", wins.Load())
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM game_buddies WHERE character_id=$1`, thirdID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("offline target count=%d %v", count, err)
	}
	winnerActor, loserActor := actors[winner.Load()], actors[1-winner.Load()]
	if _, err = s.db.ExecContext(ctx, `UPDATE game_buddy_profiles SET capacity=1 WHERE character_id=$1`, winnerActor.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MutateBuddy(ctx, domain.BuddyRequest{Session: winnerActor, Action: 1, Name: loserActor.Character.Name, Group: "Friends"}); !errors.Is(err, domain.ErrBuddyFull) {
		t.Fatalf("own capacity not checked: %v", err)
	}
}
