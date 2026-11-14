package persistence

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func TestDurableMesoDropCompetingPickupAndReplay(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	op := fmt.Sprintf("meso-drop-fixture-%d", actor.CharacterID)
	plan, err := json.Marshal(plannedCombatLoot{Items: []domain.ItemDrop{{WorldID: actor.Character.WorldID, ChannelID: actor.ChannelID, MapID: actor.Character.MapID, OwnerID: actor.CharacterID, X: 100, Y: 200, Quantity: 1, Mesos: 123}}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_rewards(operation_id,character_id,experience,character_revision) VALUES($1,$2,0,$3)`, op, actor.CharacterID, actor.Character.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_loot_jobs(operation_id,owner_id,world_id,channel_id,map_id,mob_id,x,y,loot) VALUES($1,$2,$3,$4,$5,100100,100,200,$6)`, op, actor.CharacterID, actor.Lease.WorldID, actor.ChannelID, actor.Character.MapID, plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	drops, err := store.MaterializeCombatLoot(ctx, actor.Lease, op)
	if err != nil || len(drops) != 1 || drops[0].Mesos != 123 || drops[0].TemplateID != 0 {
		t.Fatalf("ground meso materialization: %+v %v", drops, err)
	}
	loaded, err := store.ListItemDrops(ctx, actor.Lease.WorldID, actor.ChannelID, actor.Character.MapID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range loaded {
		found = found || d.ObjectID == drops[0].ObjectID && d.Mesos == 123
	}
	if !found {
		t.Fatal("ground currency did not survive reload")
	}
	var wg sync.WaitGroup
	winners := make(chan int, 2)
	for i := range actors {
		actors[i].Character.X, actors[i].Character.Y = 100, 200
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := store.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[i], ObjectID: drops[0].ObjectID})
			if err == nil {
				if result.Character.Mesos != 1123 || len(result.Changes) != 0 {
					t.Errorf("meso pickup incorrectly changed inventory: %+v", result)
				}
				winners <- i
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
		t.Fatalf("currency pickup winners=%d", count)
	}
	replayed, err := store.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[winner], ObjectID: drops[0].ObjectID})
	if err != nil || !replayed.Replayed || replayed.Character.Mesos != 1123 {
		t.Fatalf("currency replay duplicated value: %+v %v", replayed, err)
	}
	total := int64(0)
	for _, a := range actors {
		value, err := store.GetCharacter(ctx, a.CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		total += value.Mesos
	}
	if total != 2123 {
		t.Fatalf("currency not conserved: %d", total)
	}
}
