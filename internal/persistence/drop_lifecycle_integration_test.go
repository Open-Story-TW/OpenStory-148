package persistence

import (
	"errors"
	"testing"

	"maple-go-server/internal/domain"
)

func TestDropExpiryRejectsPickupRetainsAuditAndFencesOwner(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	dropRequest := domain.InventoryMoveRequest{Session: actor, ClientTick: 501, InvType: 2, Source: 1, Destination: 0, Quantity: 1}
	dropped, err := store.MoveInventory(ctx, dropRequest)
	if err != nil || dropped.Drop == nil {
		t.Fatalf("drop: %+v %v", dropped, err)
	}
	actor.Character = dropped.Character
	id := dropped.Drop.ObjectID
	var ttl float64
	if err = store.db.QueryRowContext(ctx, `SELECT extract(epoch FROM (expires_at-created_at)) FROM asset_item_drops WHERE object_id=$1`, id).Scan(&ttl); err != nil || ttl < 119 || ttl > 121 {
		t.Fatalf("legacy two-minute expiry: %f %v", ttl, err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE asset_item_drops SET expires_at=clock_timestamp()-interval '1 second' WHERE object_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PickupItem(ctx, domain.ItemPickupRequest{Session: actor, ObjectID: id}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired item was pickable before worker: %v", err)
	}
	listed, err := store.ListItemDrops(ctx, actor.Lease.WorldID, actor.ChannelID, actor.Character.MapID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("expired item survived a reconnect snapshot: %+v %v", listed, err)
	}
	stale := actor.Lease
	stale.Epoch++
	if _, err = store.ExpireItemDrops(ctx, stale, 10); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale expiry owner accepted: %v", err)
	}
	// A pending pickup owns the same row lock. Maintenance skips it instead
	// of blocking the entire world's expiration queue.
	locked, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer locked.Rollback()
	if _, err = locked.ExecContext(ctx, `SELECT object_id FROM asset_item_drops WHERE object_id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	if got, e := store.ExpireItemDrops(ctx, actor.Lease, 10); e != nil || len(got) != 0 {
		t.Fatalf("maintenance did not skip locked pickup: %+v %v", got, e)
	}
	if err = locked.Rollback(); err != nil {
		t.Fatal(err)
	}
	expired, err := store.ExpireItemDrops(ctx, actor.Lease, 10)
	if err != nil || len(expired) != 1 || expired[0].ObjectID != id || expired[0].ChannelID != actor.ChannelID || expired[0].MapID != actor.Character.MapID {
		t.Fatalf("expiry identity: %+v %v", expired, err)
	}
	if got, e := store.ExpireItemDrops(ctx, actor.Lease, 10); e != nil || len(got) != 0 {
		t.Fatalf("expiration broadcast was repeated: %+v %v", got, e)
	}
	if _, err = store.PickupItem(ctx, domain.ItemPickupRequest{Session: actors[1], ObjectID: id}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other account revived expired item: %v", err)
	}
	replay, err := store.MoveInventory(ctx, dropRequest)
	if err != nil || !replay.Replayed || replay.Character.Revision != actor.Character.Revision {
		t.Fatalf("drop replay mutated inventory: %+v %v", replay, err)
	}
	var retained, terminal bool
	if err = store.db.QueryRowContext(ctx, `SELECT true, expired_at IS NOT NULL AND picked_up_by IS NULL FROM asset_item_drops WHERE object_id=$1`, id).Scan(&retained, &terminal); err != nil || !retained || !terminal {
		t.Fatalf("expiry audit row lost: %t %t %v", retained, terminal, err)
	}
}

func TestExpiryLeavesPickedAndOtherWorldDropsAlone(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	dropped, err := store.MoveInventory(ctx, domain.InventoryMoveRequest{Session: actor, ClientTick: 502, InvType: 2, Source: 1, Destination: 0, Quantity: 1})
	if err != nil || dropped.Drop == nil {
		t.Fatalf("drop: %+v %v", dropped, err)
	}
	actor.Character = dropped.Character
	picked, err := store.PickupItem(ctx, domain.ItemPickupRequest{Session: actor, ObjectID: dropped.Drop.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	actor.Character = picked.Character
	if _, err = store.db.ExecContext(ctx, `UPDATE asset_item_drops SET expires_at=clock_timestamp()-interval '1 second' WHERE object_id=$1`, dropped.Drop.ObjectID); err != nil {
		t.Fatal(err)
	}
	var otherID int32
	err = store.db.QueryRowContext(ctx, `INSERT INTO asset_item_drops(world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity,expires_at) VALUES(123,1,10000,$1,0,0,2,2000000,1,clock_timestamp()-interval '1 second') RETURNING object_id`, actor.CharacterID).Scan(&otherID)
	if err != nil {
		t.Fatal(err)
	}
	if got, e := store.ExpireItemDrops(ctx, actor.Lease, 10); e != nil || len(got) != 0 {
		t.Fatalf("expired another world's or picked item: %+v %v", got, e)
	}
	replay, err := store.PickupItem(ctx, domain.ItemPickupRequest{Session: actor, ObjectID: dropped.Drop.ObjectID})
	if err != nil || !replay.Replayed || replay.Character.Revision != actor.Character.Revision {
		t.Fatalf("expiry broke a successful pickup receipt: %+v %v", replay, err)
	}
	var untouched bool
	if err = store.db.QueryRowContext(ctx, `SELECT expired_at IS NULL FROM asset_item_drops WHERE object_id=$1`, otherID).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("other world changed: %t %v", untouched, err)
	}
}
