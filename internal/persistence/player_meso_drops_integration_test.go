package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func TestPlayerMesoDropPersistsAndCompetingPickupBalancesLedger(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	req := domain.MesoDropRequest{Session: actor, OperationID: fmt.Sprintf("player-meso-drop-%d", actor.CharacterID), Amount: 100}
	first, err := store.DropMesosOnMap(ctx, req)
	if err != nil || first.Drop == nil || !first.Drop.PlayerDrop || first.Character.Mesos != 900 || first.Drop.ObjectID < 1000000000 {
		t.Fatalf("drop/debit: %+v %v", first, err)
	}
	replay, err := store.DropMesosOnMap(ctx, req)
	if err != nil || !replay.Replayed || replay.Drop.ObjectID != first.Drop.ObjectID || replay.Character.Mesos != 900 {
		t.Fatalf("drop replay: %+v %v", replay, err)
	}
	req.Amount = 101
	if _, err = store.DropMesosOnMap(ctx, req); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("replay changed amount: %v", err)
	}
	listed, err := store.ListItemDrops(ctx, actor.Lease.WorldID, actor.ChannelID, actor.Character.MapID)
	if err != nil || len(listed) != 1 || listed[0].Mesos != 100 || !listed[0].PlayerDrop {
		t.Fatalf("ground currency not reloadable: %+v %v", listed, err)
	}
	actors[0].Character = first.Character
	var wg sync.WaitGroup
	winners := make(chan int, 2)
	for i := range actors {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := store.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[i], ObjectID: first.Drop.ObjectID})
			if err == nil {
				actors[i].Character = result.Character
				winners <- i
			} else if !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("pickup: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(winners)
	winner, count := -1, 0
	for i := range winners {
		winner, count = i, count+1
	}
	if count != 1 {
		t.Fatalf("pickup winners: %d", count)
	}
	if again, err := store.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[winner], ObjectID: first.Drop.ObjectID}); err != nil || !again.Replayed {
		t.Fatalf("pickup replay: %+v %v", again, err)
	}
	var balance, delta int64
	if err := store.db.QueryRowContext(ctx, `SELECT sum(mesos) FROM game_characters WHERE id IN($1,$2)`, actors[0].CharacterID, actors[1].CharacterID).Scan(&balance); err != nil || balance != 2000 {
		t.Fatalf("currency balance=%d %v", balance, err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT sum(delta) FROM asset_meso_ledger WHERE operation_id IN($1,$2)`, req.OperationID, fmt.Sprintf("ground-meso-pickup-%d", first.Drop.ObjectID)).Scan(&delta); err != nil || delta != 0 {
		t.Fatalf("drop/pickup ledger delta=%d %v", delta, err)
	}
}

func TestPlayerMesoDropInsertionFailureDoesNotDebit(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	name := fmt.Sprintf("meso_drop_fail_%d", actor.CharacterID)
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test drop insertion failure'; END $$; CREATE TRIGGER %s BEFORE INSERT ON asset_item_drops FOR EACH ROW WHEN (NEW.owner_id=%d) EXECUTE FUNCTION %s()`, name, name, actor.CharacterID, name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON asset_item_drops; DROP FUNCTION IF EXISTS %s()", name, name))
	})
	op := fmt.Sprintf("player-meso-failure-%d", actor.CharacterID)
	if _, err := store.DropMesosOnMap(ctx, domain.MesoDropRequest{Session: actor, OperationID: op, Amount: 100}); err == nil {
		t.Fatal("injected insertion failure accepted")
	}
	loaded, err := store.GetCharacter(ctx, actor.CharacterID)
	if err != nil || loaded.Mesos != actor.Character.Mesos || loaded.Revision != actor.Character.Revision {
		t.Fatalf("money was debited without a drop: %+v %v", loaded, err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_meso_ledger WHERE operation_id=$1`, op).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed debit left ledger: %d %v", count, err)
	}
}
