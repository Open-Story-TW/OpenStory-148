package persistence

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func TestMonsterEquipmentLootPinsStatsAndAllocatesOnce(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	root := t.TempDir()
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	for path, data := range map[string]string{
		"Character.wz/Cap/01002067.img.xml": `<imgdir name="01002067.img"><imgdir name="info"><int name="incPDD" value="50"/><int name="tuc" value="7"/></imgdir></imgdir>`,
		"Mob.wz/0100100.img.xml":            `<imgdir name="0100100.img"><imgdir name="info"><int name="level" value="1"/></imgdir></imgdir>`,
	} {
		file := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	actor := actors[0]
	operation := ""
	for i := range 10000 {
		candidate := fmt.Sprintf("equipment-loot-%d-%d", actor.CharacterID, i)
		if roll := lootRoll(candidate, 13063, 0, 999999); roll >= 1500 && roll < 15000 {
			operation = candidate
			break
		}
	}
	if operation == "" {
		t.Fatal("unable to seed an equipment drop")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_combat_rewards(operation_id,character_id,experience,character_revision) VALUES($1,$2,0,$3)`, operation, actor.CharacterID, actor.Character.Revision); err != nil {
		t.Fatal(err)
	}
	if err = enqueueCombatLootTx(ctx, tx, actor, actor.Character, operation, 100100, actor.Character.X, actor.Character.Y); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	drops, err := store.MaterializeCombatLoot(ctx, actor.Lease, operation)
	if err != nil || len(drops) != 1 || drops[0].TemplateID != 1002067 || drops[0].Equipment == nil {
		t.Fatalf("expected the rolled Snail equipment: %+v %v", drops, err)
	}
	drop := drops[0]
	want := *drop.Equipment
	if want.WDEF < 45 || want.WDEF > 55 || want.STR != 0 || want.UpgradeSlots != 7 || want.ID != 0 {
		t.Fatalf("unexpected dropped stats: %+v", want)
	}
	cached, err := store.ListItemDrops(ctx, actor.Lease.WorldID, actor.ChannelID, actor.Character.MapID)
	if err != nil || len(cached) != 1 || cached[0].Equipment == nil || *cached[0].Equipment != want {
		t.Fatalf("drop snapshot did not preserve the roll: %+v %v", cached, err)
	}
	req := domain.ItemPickupRequest{Session: actor, ObjectID: drop.ObjectID}
	picked, err := store.PickupItem(ctx, req)
	if err != nil || len(picked.Changes) != 1 || picked.Changes[0].Equipment == nil {
		t.Fatalf("equipment pickup failed: %+v %v", picked.Changes, err)
	}
	created := *picked.Changes[0].Equipment
	if created.ID <= 0 || created.Slot <= 0 || created.WDEF != want.WDEF {
		t.Fatalf("pickup changed the rolled equipment: %+v", created)
	}
	repeat, err := store.PickupItem(ctx, req)
	if err != nil || !repeat.Replayed {
		t.Fatalf("equipment pickup replay: %+v %v", repeat, err)
	}
	count := 0
	for _, e := range repeat.Character.Equipment {
		if e.ItemID == want.ItemID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("equipment pickup allocated %d copies", count)
	}
}

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
