package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func TestNativeCashMoveBindsInventoryTypeAndTargetSlot(t *testing.T) {
	ctx, store, actors := inventoryFixture(t)
	actor := actors[0]
	lease := cashTestLease(t, ctx, store, actor.CharacterID)
	handle := actor.CharacterID + 100000
	if _, err := store.BeginCashTransfer(ctx, actor, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := store.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := store.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,state) VALUES($1,$2,1,2000000,2,2,'locker') RETURNING id`, actor.AccountID, actor.Lease.WorldID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	request := domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("test-cash-target-%d", id), CashID: id, InvType: 3, TargetSlot: 3}
	if _, err = store.MoveCashItem(ctx, request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("wrong inventory type: %v", err)
	}
	request.InvType, request.TargetSlot = 2, 1
	if _, err = store.MoveCashItem(ctx, request); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("occupied target slot: %v", err)
	}
	request.TargetSlot = 25
	if _, err = store.MoveCashItem(ctx, request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("target over capacity: %v", err)
	}
	request.TargetSlot = 3
	result, err := store.MoveCashItem(ctx, request)
	if err != nil || result.Item.Slot != 3 || result.Item.Quantity != 2 {
		t.Fatalf("exact target placement: %+v %v", result, err)
	}
	if replay, err := store.MoveCashItem(ctx, request); err != nil || !replay.Replayed || replay.Item.Slot != 3 {
		t.Fatalf("move replay: %+v %v", replay, err)
	}
	request.TargetSlot = 4
	if _, err = store.MoveCashItem(ctx, request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("replay changed target: %v", err)
	}
	request.OperationID += "-back"
	request.ToLocker, request.TargetSlot, request.InvType = true, 0, 3
	if _, err = store.MoveCashItem(ctx, request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("deposit changed inventory type: %v", err)
	}
	request.InvType = 2
	result, err = store.MoveCashItem(ctx, request)
	if err != nil || result.Item.State != "locker" || result.Item.Quantity != 2 {
		t.Fatalf("deposit: %+v %v", result, err)
	}
}

func TestUnmappedCashPurchaseOptionRejectsBeforeSpending(t *testing.T) {
	store := &Store{}
	_, err := store.PurchaseCash(t.Context(), domain.CashPurchaseRequest{OperationID: "unsupported-option-test", Currency: 1, SN: 10000049, Option: 7})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("unknown purchase option accepted: %v", err)
	}
}
