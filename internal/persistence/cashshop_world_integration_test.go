package persistence

import (
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestCashCrossWorldLockerStorageAndExpiryMetadata(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	lease := cashTestLease(t, ctx, s, a.CharacterID)
	handle := a.CharacterID + 100000
	if _, err := s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil {
		t.Fatal(err)
	}
	var cashID int64
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,state) VALUES($1,123,1,2000000,3,2,$2,'locker') RETURNING id`, a.AccountID, time.Now().Add(time.Hour).UnixMilli()).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	assertWorld := func(id int64, state string) {
		t.Helper()
		var world int
		var actual string
		if e := s.db.QueryRowContext(ctx, `SELECT world_id,state FROM asset_cash_items WHERE id=$1`, id).Scan(&world, &actual); e != nil || world != 124 || state != actual {
			t.Fatalf("cash %d world=%d state=%s expected124/%s err%v", id, world, actual, state, e)
		}
	}
	poison := func(id int64) {
		t.Helper()
		if _, e := s.db.ExecContext(ctx, `UPDATE asset_cash_items SET world_id=123 WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	move := func(toLocker bool, seq int) {
		t.Helper()
		r, e := s.MoveCashItem(ctx, domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("cross-world-cash-%d-%d", cashID, seq), CashID: cashID, ToLocker: toLocker})
		if e != nil {
			t.Fatal(e)
		}
		cs.Session.Character = r.Character
	}
	move(false, 1)
	assertWorld(cashID, "inventory")
	poison(cashID)
	move(true, 2)
	assertWorld(cashID, "locker")
	move(false, 3)
	if _, err = s.BeginCashReturn(ctx, cs, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	a, _, err = s.ConsumeCashReturn(ctx, handle, 124, 1, "127.0.0.1", a.Lease)
	if err != nil {
		t.Fatal(err)
	}
	// An ordinary inventory relocation also repairs legacy stale world metadata.
	poison(cashID)
	relocated, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 15000, InvType: 2, Source: 2, Destination: 3})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = relocated.Character
	assertWorld(cashID, "inventory")
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	poison(cashID)
	deposit, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("cross-world-deposit-%d", cashID), Kind: "deposit", Slot: 3, TemplateID: 2000000, Quantity: 2, ExpectedRevision: storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = deposit.Inventory.Character
	stored := deposit.Storage.Items[0]
	assertWorld(cashID, "inventory")
	assertWorld(stored.Item.CashID, "storage")
	poison(stored.Item.CashID)
	withdraw, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("cross-world-withdraw-%d", cashID), Kind: "withdraw", StorageItemID: stored.ID, ExpectedRevision: deposit.Storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = withdraw.Inventory.Character
	assertWorld(stored.Item.CashID, "inventory")
	slot := 0
	for _, i := range a.Character.Items {
		if i.CashID == stored.Item.CashID {
			slot = i.Slot
		}
	}
	restored, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("cross-world-redeposit-%d", cashID), Kind: "deposit", Slot: slot, TemplateID: 2000000, Quantity: 2, ExpectedRevision: withdraw.Storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = restored.Inventory.Character
	// Expiry must repair and tombstone even already-stored legacy metadata.
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_items SET world_id=123,expires_at=1 WHERE id=$1`, stored.Item.CashID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_storage_items SET expires_at=1 WHERE cash_id=$1`, stored.Item.CashID); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.OpenStorage(ctx, a)
	if err != nil || len(pruned.Items) != 0 {
		t.Fatalf("expired storage=%+v err%v", pruned, err)
	}
	assertWorld(stored.Item.CashID, "expired")
}
