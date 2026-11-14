package persistence

import (
	"fmt"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func TestCombatLootDurableQueueAndCompetingPickup(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	operation := ""
	for i := 0; i < 100; i++ {
		candidate := fmt.Sprintf("loot-regression-%d-%d", actor.CharacterID, i)
		if lootRoll(candidate, 13055, 0, 999999) < 600000 {
			operation = candidate
			break
		}
	}
	if operation == "" {
		t.Fatal("could not select the known shell-drop fixture")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_rewards(operation_id,character_id,experience,character_revision) VALUES($1,$2,0,$3)`, operation, actor.CharacterID, actor.Character.Revision); err != nil {
		t.Fatal(err)
	}
	if err = enqueueCombatLootTx(ctx, tx, actor, actor.Character, operation, 100100, 100, 200); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingCombatLoot(ctx, actor.Lease, 64)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range pending {
		found = found || id == operation
	}
	if !found {
		t.Fatal("committed kill did not retain its loot job")
	}
	stale := actor.Lease
	stale.Epoch++
	if _, err = store.MaterializeCombatLoot(ctx, stale, operation); err == nil {
		t.Fatal("stale owner materialized a pending job")
	}
	drops, err := store.MaterializeCombatLoot(ctx, actor.Lease, operation)
	if err != nil {
		t.Fatal(err)
	}
	var shell domain.ItemDrop
	for _, drop := range drops {
		if drop.TemplateID == 4000019 {
			shell = drop
		}
	}
	if shell.ObjectID < 1000000000 || shell.Quantity != 1 {
		t.Fatalf("pinned shell outcome was lost: %+v", drops)
	}
	if again, err := store.MaterializeCombatLoot(ctx, actor.Lease, operation); err != nil || len(again) != 0 {
		t.Fatalf("loot replay produced new objects: %+v %v", again, err)
	}
	var wg sync.WaitGroup
	winner := make(chan int, 2)
	for i := range actors {
		actors[i].Character.X, actors[i].Character.Y = 100, 200
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[i], ObjectID: shell.ObjectID}); err == nil {
				winner <- i
			}
		}(i)
	}
	wg.Wait()
	close(winner)
	count := 0
	for range winner {
		count++
	}
	if count != 1 {
		t.Fatalf("competing loot pickup winners=%d", count)
	}
	quantity := 0
	for _, actor := range actors {
		value, err := store.GetCharacter(ctx, actor.CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range value.Items {
			if item.TemplateID == 4000019 {
				quantity += item.Quantity
			}
		}
	}
	if quantity != 1 {
		t.Fatalf("loot quantity after racing pickup=%d", quantity)
	}
}
