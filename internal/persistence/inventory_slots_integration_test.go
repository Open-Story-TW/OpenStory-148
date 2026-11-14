package persistence

import (
	"context"
	"errors"
	"fmt"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func expandTestBag(t *testing.T, ctx context.Context, s *Store, session *domain.CharacterSession, invType, delta int) {
	t.Helper()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, *session)
	if err != nil {
		t.Fatal(err)
	}
	if err = ExpandInventorySlotsSQL(ctx, tx, &c, invType, delta); err != nil {
		t.Fatal(err)
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	session.Character = c
}

func TestExpandedInventoryBundleMoveUseShopStorageTradeDropAndScript(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	for _, session := range []domain.CharacterSession{a, b} {
		if _, err := s.db.ExecContext(ctx, `UPDATE asset_items SET quantity=300 WHERE character_id=$1`, session.CharacterID); err != nil {
			t.Fatal(err)
		}
		for slot := 2; slot <= 24; slot++ {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,$2,2000000,300)`, session.CharacterID, slot); err != nil {
				t.Fatal(err)
			}
		}
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	b.Character, err = s.GetCharacter(ctx, b.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	shop, err := content.NPCShop(2)
	if err != nil {
		t.Fatal(err)
	}
	var entry domain.ShopItem
	for _, item := range shop.Items {
		if item.TemplateID == 2000000 {
			entry = item
			break
		}
	}
	buy := domain.ShopRequest{Session: a, OperationID: fmt.Sprintf("capacity-buy-fail-%d", a.CharacterID), ShopID: 2, Kind: "buy", EntryID: entry.ID, TemplateID: 2000000, Quantity: 2}
	if _, err = s.ChangeShop(ctx, buy); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("default-full buy=%v", err)
	}
	expandTestBag(t, ctx, s, &a, 2, 4)
	if a.Character.Capacity(2) != 28 || a.Character.Capacity(1) != 24 || b.Character.Capacity(2) != 24 {
		t.Fatal("bag extension leaked")
	}
	buy.Session = a
	buy.OperationID = fmt.Sprintf("capacity-buy-pass-%d", a.CharacterID)
	purchase, err := s.ChangeShop(ctx, buy)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = purchase.Inventory.Character
	if len(a.Character.Items) != 25 || purchase.Inventory.Changes[0].Slot != 25 {
		t.Fatal("shop ignored expanded slot")
	}
	move, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 31000, InvType: 2, Source: 25, Destination: 28})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = move.Character
	if _, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 31001, InvType: 2, Source: 28, Destination: 29}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("capacity overrun=%v", err)
	}
	a.Character, err = s.UseItem(ctx, domain.UseItemRequest{Session: a, ClientTick: 31002, Slot: 28, TemplateID: 2000000})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	deposit, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("capacity-storage-deposit-%d", a.CharacterID), Kind: "deposit", Slot: 28, TemplateID: 2000000, Quantity: 1, ExpectedRevision: storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = deposit.Inventory.Character
	withdraw, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("capacity-storage-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: deposit.Storage.Items[0].ID, ExpectedRevision: deposit.Storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = withdraw.Inventory.Character
	if withdraw.Inventory.Changes[0].Slot != 25 {
		t.Fatal("storage ignored expanded slot")
	}
	var offered domain.TradeItem
	for _, item := range a.Character.Items {
		if item.Slot == 25 {
			offered = domain.TradeItem{InvType: 2, Slot: 25, Quantity: 1, InstanceID: item.ID, TradeSlot: 1}
		}
	}
	trade := domain.TradeSettlement{ID: fmt.Sprintf("capacity-trade-%d", a.CharacterID), First: a, Second: b, FirstOffer: domain.TradeOffer{Items: []domain.TradeItem{offered}}}
	if _, err = s.SettleTrade(ctx, trade); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("default-full recipient=%v", err)
	}
	expandTestBag(t, ctx, s, &b, 2, 4)
	trade.Second = b
	settled, err := s.SettleTrade(ctx, trade)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = settled.First.Character
	b.Character = settled.Second.Character
	if settled.Second.Changes[0].Slot != 25 {
		t.Fatal("trade ignored expanded slot")
	}
	drop, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: b, ClientTick: 31003, InvType: 2, Source: 25, Destination: 0, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	b.Character = drop.Character
	pickup, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: a, ObjectID: drop.Drop.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = pickup.Character
	if pickup.Changes[0].Slot != 25 {
		t.Fatal("drop pickup ignored expanded slot")
	}
	// Script rewards fill a stack and allocate another expanded slot in one tx.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, a)
	if err != nil {
		t.Fatal(err)
	}
	result := domain.InventoryResult{Character: c}
	if err = applyScriptItem(ctx, tx, &result, domain.ScriptItemDelta{TemplateID: 2000000, Quantity: 301}); err != nil {
		t.Fatal(err)
	}
	if err = commitInventoryState(ctx, tx, &result.Character); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a.Character = result.Character
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.Capacity(2) != 28 || len(loaded.Items) != 26 {
		t.Fatalf("durable extended bag=%d/%d err%v", loaded.Capacity(2), len(loaded.Items), err)
	}
	// Database capacity is authoritative even if the session claims a larger bag.
	a.Character.InventorySlots[1] = 96
	if _, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 31004, InvType: 2, Source: 26, Destination: 29}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("forged capacity accepted=%v", err)
	}
}

func TestExpandedEquipmentCapacityAndRollback(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	for slot := 1; slot <= 24; slot++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot) VALUES($1,1322005,$2)`, a.CharacterID, slot); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	if FreeEquipmentSlot(&a.Character) != 0 {
		t.Fatal("default-full equipment not full")
	}
	expandTestBag(t, ctx, s, &a, 1, 4)
	if FreeEquipmentSlot(&a.Character) != 25 {
		t.Fatal("expanded equipment allocation")
	}
	move, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 32000, InvType: 1, Source: -11, Destination: 28})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = move.Character
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	deposit, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("capacity-equip-storage-%d", a.CharacterID), Kind: "deposit", Slot: 28, TemplateID: 1322005, Quantity: 1, ExpectedRevision: storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = deposit.Inventory.Character
	withdraw, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("capacity-equip-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: deposit.Storage.Items[0].ID, ExpectedRevision: deposit.Storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = withdraw.Inventory.Character
	if withdraw.Inventory.Changes[0].Slot != 25 {
		t.Fatal("equipment storage ignored capacity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := lockInventoryCharacter(ctx, tx, a)
	if err != nil {
		t.Fatal(err)
	}
	if err = ExpandInventorySlotsSQL(ctx, tx, &c, 1, 96); err != nil || c.Capacity(1) != 96 {
		t.Fatalf("clamp=%d %v", c.Capacity(1), err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.Capacity(1) != 28 || loaded.Capacity(4) != 24 {
		t.Fatal("capacity escaped rollback", err)
	}
}

func TestExpandedInventoryCashLockerAndIndependentBagTypes(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	// Explicit fixture inventory: all original cash bag positions are occupied.
	for slot := 1; slot <= 24; slot++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,5,$2,5072000,1)`, a.CharacterID, slot); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	for invType := 1; invType <= 5; invType++ {
		expandTestBag(t, ctx, s, &a, invType, 4)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	for invType := 1; invType <= 5; invType++ {
		if loaded.Capacity(invType) != 28 {
			t.Fatalf("bag%d capacity=%d", invType, loaded.Capacity(invType))
		}
	}
	lease := cashTestLease(t, ctx, s, a.CharacterID)
	handle := a.CharacterID + 100000
	if _, err = s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil {
		t.Fatal(err)
	}
	var cashID int64
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,state) VALUES($1,124,1,5072000,1,5,$2,'locker') RETURNING id`, a.AccountID, time.Now().Add(time.Hour).UnixMilli()).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	r := domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("capacity-cash-withdraw-%d", a.CharacterID), CashID: cashID}
	withdraw, err := s.MoveCashItem(ctx, r)
	if err != nil || withdraw.Item.Slot != 25 || withdraw.Character.Capacity(5) != 28 {
		t.Fatalf("cash extended slot=%d err%v", withdraw.Item.Slot, err)
	}
	cs.Session.Character = withdraw.Character
	replay, err := s.MoveCashItem(ctx, r)
	if err != nil || !replay.Replayed || replay.Character.Revision != withdraw.Character.Revision {
		t.Fatal("cash replay", err)
	}
	if _, err = s.BeginCashReturn(ctx, cs, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	a, _, err = s.ConsumeCashReturn(ctx, handle, 124, 1, "127.0.0.1", a.Lease)
	if err != nil {
		t.Fatal(err)
	}
	move, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 33000, InvType: 5, Source: 25, Destination: 28})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = move.Character
	if _, err = s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil {
		t.Fatal(err)
	}
	deposit, err := s.MoveCashItem(ctx, domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("capacity-cash-deposit-%d", a.CharacterID), CashID: cashID, ToLocker: true})
	if err != nil || deposit.Item.State != "locker" {
		t.Fatal("cash extended deposit", err)
	}
	loaded, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	for invType := 1; invType <= 5; invType++ {
		if loaded.Capacity(invType) != 28 {
			t.Fatalf("cash handoff lost bag%d", invType)
		}
	}
}
