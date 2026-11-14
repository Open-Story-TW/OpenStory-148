package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func cashTestLease(t *testing.T, ctx context.Context, s *Store, id int64) domain.ServiceLease {
	t.Helper()
	lease := domain.ServiceLease{Service: "cashshop"}
	err := s.db.QueryRowContext(ctx, `SELECT instance_id,epoch,expires_at FROM ops_service_leases WHERE service='cashshop' AND expires_at>clock_timestamp()`).Scan(&lease.InstanceID, &lease.Epoch, &lease.ExpiresAt)
	if err == nil {
		return lease
	}
	lease, err = s.AcquireCashLease(ctx, fmt.Sprintf("cash-test-%d", id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.ReleaseCashLease(context.Background(), lease)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM ops_service_leases WHERE service='cashshop' AND instance_id=$1`, lease.InstanceID)
	})
	return lease
}

func TestCashTransferFencesAndReturnsOnce(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	lease := cashTestLease(t, ctx, s, a.CharacterID)
	handle := a.CharacterID + 100000
	transfer, err := s.BeginCashTransfer(ctx, a, handle, "127.0.0.1")
	if err != nil || transfer.NativeHandle != handle {
		t.Fatalf("begin=%+v %v", transfer, err)
	}
	if _, err = s.AdjustMesos(ctx, domain.MesoAdjustment{Session: a, Delta: 1, OperationID: "cash-test-old-owner", Reason: "test"}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("old world owner accepted: %v", err)
	}
	if _, err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.2", lease); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("wrong IP: %v", err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil || cs.Session.Generation <= a.Generation || cs.NativeHandle != handle {
		t.Fatalf("claim=%+v %v", cs, err)
	}
	if _, err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease); !errors.Is(err, domain.ErrTransferInvalid) {
		t.Fatalf("double claim=%v", err)
	}
	if err = s.ReleaseCharacter(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadCashWallet(ctx, cs); err != nil {
		t.Fatalf("old world cleanup destroyed cash session: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=$1`, a.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginLogin(ctx, a.AccountID, "duplicate-during-live-cashshop"); !errors.Is(err, domain.ErrAlreadyOnline) {
		t.Fatalf("live cash lease must reject login despite stale account expiry: %v", err)
	}
	forged := cs
	forged.ReturnChannel++
	if _, err = s.ReadCashWallet(ctx, forged); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("forged return channel accepted: %v", err)
	}
	forged = cs
	forged.Session.Lease.WorldID++
	if _, err = s.ReadCashWallet(ctx, forged); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("forged world accepted: %v", err)
	}
	back, err := s.BeginCashReturn(ctx, cs, "127.0.0.1")
	if err != nil || back.NativeHandle != handle {
		t.Fatalf("begin return=%+v %v", back, err)
	}
	if err = s.ReleaseCashCharacter(ctx, cs); err != nil {
		t.Fatal(err)
	}
	returned, handled, err := s.ConsumeCashReturn(ctx, handle, a.Lease.WorldID, 1, "127.0.0.1", a.Lease)
	if err != nil || !handled || returned.CharacterID != a.CharacterID || returned.Generation <= cs.Session.Generation {
		t.Fatalf("return=%+v handled%v %v", returned, handled, err)
	}
	if _, handled, err = s.ConsumeCashReturn(ctx, handle, a.Lease.WorldID, 1, "127.0.0.1", a.Lease); err != nil || handled {
		t.Fatalf("replayed return handled=%v %v", handled, err)
	}
}

func TestCashBundleConsumptionStorageAndLockerConserve(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	lease := cashTestLease(t, ctx, s, a.CharacterID)
	var cashID int64
	expiry := time.Now().Add(time.Hour).UnixMilli()
	// A test-only paid bundle fixture: production grants only through PurchaseCash.
	if err := s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,expires_at,state) VALUES($1,$2,$3,1,2000000,5,2,1,$4,'inventory') RETURNING id`, a.AccountID, a.CharacterID, a.Lease.WorldID, expiry).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.UseItem(ctx, domain.UseItemRequest{Session: a, ClientTick: 12001, Slot: 1, TemplateID: 2000000})
	if err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err = s.db.QueryRowContext(ctx, `SELECT quantity FROM asset_cash_items WHERE id=$1`, cashID).Scan(&remaining); err != nil || remaining != 4 {
		t.Fatalf("consumed bundle ledger quantity=%d err=%v", remaining, err)
	}
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	deposit, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("cash-storage-deposit-%d", a.CharacterID), Kind: "deposit", Slot: 1, TemplateID: 2000000, Quantity: 2, ExpectedRevision: storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = deposit.Inventory.Character
	stored := deposit.Storage.Items[0]
	if stored.Item.CashID == 0 || stored.Item.CashID == cashID || stored.Item.ExpiresAt != expiry {
		t.Fatalf("split storage metadata=%+v", stored)
	}
	withdraw, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("cash-storage-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: stored.ID, ExpectedRevision: deposit.Storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = withdraw.Inventory.Character
	for tick := 12002; tick <= 12003; tick++ {
		a.Character, err = s.UseItem(ctx, domain.UseItemRequest{Session: a, ClientTick: uint32(tick), Slot: 1, TemplateID: 2000000})
		if err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err = s.db.QueryRowContext(ctx, `SELECT state FROM asset_cash_items WHERE id=$1`, cashID).Scan(&state); err != nil || state != "expired" {
		t.Fatalf("consumed serial state=%s err=%v", state, err)
	}
	moved, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 12004, InvType: 2, Source: 2, Destination: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = moved.Character
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].CashID != stored.Item.CashID || loaded.Items[0].Quantity != 2 || loaded.Items[0].ExpiresAt != expiry {
		t.Fatalf("reused slot metadata=%+v err=%v", loaded.Items, err)
	}
	handle := a.CharacterID + 100000
	if _, err = s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
	if err != nil {
		t.Fatal(err)
	}
	locker, err := s.MoveCashItem(ctx, domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("cash-conserved-locker-%d", a.CharacterID), CashID: stored.Item.CashID, ToLocker: true})
	if err != nil || locker.Item.Quantity != 2 || locker.Item.ExpiresAt != expiry {
		t.Fatalf("locker resurrected quantity: %+v err=%v", locker.Item, err)
	}
}

func TestCashPurchaseLockerReplayAndExpiry(t *testing.T) {
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
	root := t.TempDir()
	path := filepath.Join(root, "Character.wz", "Accessory")
	if err = os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir name="01022009.img"><imgdir name="info"><int name="cash" value="1"/><int name="incDEX" value="2"/><int name="tuc" value="0"/></imgdir></imgdir>`
	if err = os.WriteFile(filepath.Join(path, "01022009.img.xml"), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	req := domain.CashPurchaseRequest{Session: cs, OperationID: fmt.Sprintf("cash-purchase-operation-%d", a.CharacterID), SN: 10000049, Currency: 1}
	if _, err = s.PurchaseCash(ctx, req); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("zero wallet buy=%v", err)
	}
	if _, err = s.ReadCashWallet(ctx, cs); err != nil {
		t.Fatal(err)
	}
	// Explicit test-only funding; production never mints initial NX.
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=1000 WHERE account_id=$1`, a.AccountID); err != nil {
		t.Fatal(err)
	}
	bought, err := s.PurchaseCash(ctx, req)
	if err != nil || bought.Wallet.Credit != 945 || bought.Item.TemplateID != 1022009 {
		t.Fatalf("purchase=%+v %v", bought, err)
	}
	replay, err := s.PurchaseCash(ctx, req)
	if err != nil || !replay.Replayed || replay.Wallet.Credit != 945 || replay.Item.ID != bought.Item.ID {
		t.Fatalf("purchase replay=%+v %v", replay, err)
	}
	move := domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("cash-withdraw-operation-%d", a.CharacterID), CashID: bought.Item.ID}
	got, err := s.MoveCashItem(ctx, move)
	if err != nil || got.Item.State != "inventory" || equipmentAt(got.Character, 1).CashID != bought.Item.ID {
		t.Fatalf("withdraw=%+v %v", got, err)
	}
	cs.Session.Character = got.Character
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || equipmentAt(loaded, 1).CashID != bought.Item.ID {
		t.Fatalf("reload metadata=%+v %v", loaded, err)
	}
	back, err := s.MoveCashItem(ctx, domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("cash-deposit-operation-%d", a.CharacterID), CashID: bought.Item.ID, ToLocker: true})
	if err != nil || back.Item.State != "locker" {
		t.Fatalf("deposit=%+v %v", back, err)
	}
	cs.Session.Character = back.Character
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_items SET expires_at=$1 WHERE id=$2`, time.Now().Add(-time.Second).UnixMilli(), bought.Item.ID); err != nil {
		t.Fatal(err)
	}
	move.OperationID += "-expired"
	move.Session = cs
	if _, err = s.MoveCashItem(ctx, move); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("expired withdrawal=%v", err)
	}
}

func TestCashEquipmentStoragePreservesSerial(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	moved, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 13000, InvType: 1, Source: -11, Destination: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = moved.Character
	eq := equipmentAt(a.Character, 1)
	expiry := time.Now().Add(time.Hour).UnixMilli()
	var cashID int64
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,expires_at,state) VALUES($1,$2,$3,1,$4,1,1,1,$5,'inventory') RETURNING id`, a.AccountID, a.CharacterID, a.Lease.WorldID, eq.ItemID, expiry).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	deposited, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("cash-equip-deposit-%d", a.CharacterID), Kind: "deposit", Slot: 1, TemplateID: eq.ItemID, Quantity: 1, ExpectedRevision: storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = deposited.Inventory.Character
	loaded, err := s.OpenStorage(ctx, a)
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Item.CashID != cashID || loaded.Items[0].Equipment.CashID != cashID {
		t.Fatalf("stored cash equipment=%+v err=%v", loaded.Items, err)
	}
	withdrawn, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("cash-equip-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: loaded.Items[0].ID, ExpectedRevision: loaded.Revision})
	if err != nil {
		t.Fatal(err)
	}
	actual := equipmentAt(withdrawn.Inventory.Character, 1)
	if actual.ID != eq.ID || actual.CashID != cashID || actual.ExpiresAt != expiry {
		t.Fatalf("withdrawn cash equipment=%+v", actual)
	}
	reloaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || equipmentAt(reloaded, 1).CashID != cashID {
		t.Fatalf("cash equipment overlay after storage=%+v err=%v", equipmentAt(reloaded, 1), err)
	}
}

func TestCashApparelEquipAndDatabaseExpiry(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	root := t.TempDir()
	path := filepath.Join(root, "Character.wz", "Accessory")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "01022009.img.xml"), []byte(`<imgdir name="01022009.img"><imgdir name="info"><int name="cash" value="1"/><int name="incMHP" value="10"/><int name="tuc" value="0"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := domain.Equipment{ItemID: 1022009, Slot: 1, HP: 10}
	if err = insertEquipment(ctx, tx, a.CharacterID, &e); err != nil {
		t.Fatal(err)
	}
	var cashID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,expires_at,state) VALUES($1,$2,$3,1,1022009,1,1,1,$4,'inventory') RETURNING id`, a.AccountID, a.CharacterID, a.Lease.WorldID, time.Now().Add(time.Hour).UnixMilli()).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	baseHP := a.Character.MaxHP
	if _, err = s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 14000, InvType: 1, Source: 1, Destination: -3}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("cash accessory in normal slot accepted: %v", err)
	}
	worn, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 14001, InvType: 1, Source: 1, Destination: -103})
	if err != nil || equipmentAt(worn.Character, -103).CashID != cashID || worn.Character.MaxHP != baseHP+10 {
		t.Fatalf("cash equip=%+v err=%v", worn, err)
	}
	a.Character = worn.Character
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_items SET expires_at=1 WHERE id=$1`, cashID); err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireCashInventory(ctx, a)
	if err != nil || len(expired.Changes) != 1 || expired.Changes[0].Slot != -103 || expired.Character.MaxHP != baseHP || equipmentAt(expired.Character, -103).ItemID != 0 {
		t.Fatalf("expiry=%+v err=%v", expired, err)
	}
	a.Character = expired.Character
	again, err := s.ExpireCashInventory(ctx, a)
	if err != nil || len(again.Changes) != 0 || again.Character.Revision != expired.Character.Revision {
		t.Fatalf("expiry replay=%+v err=%v", again, err)
	}
}
