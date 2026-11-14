package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func scrollFixture(t *testing.T) (context.Context, *Store, []domain.CharacterSession) {
	t.Helper()
	ctx, s, sessions := inventoryFixture(t)
	root := t.TempDir()
	weapon := filepath.Join(root, "Character.wz", "Weapon")
	consume := filepath.Join(root, "Item.wz", "Consume")
	for _, path := range []string{weapon, consume} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(weapon, "01322005.img.xml"), []byte(`<imgdir name="01322005.img"><imgdir name="info"><int name="incPAD" value="19"/><int name="tuc" value="7"/></imgdir></imgdir>`), 0644); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir name="0204.img"><imgdir name="02043200"><imgdir name="info"><int name="success" value="100"/><int name="incPAD" value="2"/><int name="incMHP" value="10"/></imgdir></imgdir><imgdir name="02043201"><imgdir name="info"><int name="success" value="0"/><int name="incPAD" value="2"/></imgdir></imgdir><imgdir name="02043202"><imgdir name="info"><int name="success" value="0"/><int name="cursed" value="100"/><int name="incPAD" value="2"/></imgdir></imgdir><imgdir name="02040000"><imgdir name="info"><int name="success" value="100"/><int name="incPDD" value="10"/></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(consume, "0204.img.xml"), []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	for slot, item := range map[int]int{2: 2043200, 3: 2043201, 4: 2043202, 5: 2040000, 6: 2340000} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,$2,$3,2)`, sessions[0].CharacterID, slot, item); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	sessions[0].Character, err = s.GetCharacter(ctx, sessions[0].CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, sessions
}

func TestScrollSuccessReplayAndAssetTransferDurability(t *testing.T) {
	ctx, s, sessions := scrollFixture(t)
	a, b := sessions[0], sessions[1]
	old := equipmentAt(a.Character, -11)
	baseHP := a.Character.MaxHP
	r := domain.ScrollRequest{Session: a, ClientTick: 20001, SourceSlot: 2, TargetSlot: -11}
	var results [2]domain.ScrollResult
	var errs [2]error
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = s.ApplyScroll(ctx, r) }(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].Replayed == results[1].Replayed {
		t.Fatalf("concurrent scroll=%+v errors=%v", results, errs)
	}
	for _, result := range results {
		eq := equipmentAt(result.Inventory.Character, -11)
		if result.Status != 1 || eq.WATK != old.WATK+2 || eq.ScrollLevel != 1 || eq.UpgradeSlots != old.UpgradeSlots-1 || result.Inventory.Character.MaxHP != baseHP+10 {
			t.Fatalf("scroll result=%+v", result)
		}
	}
	a.Character = results[0].Inventory.Character
	wrong := r
	wrong.SourceSlot = 3
	if _, err := s.ApplyScroll(ctx, wrong); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("changed replay accepted %v", err)
	}
	unequip, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 20002, InvType: 1, Source: -11, Destination: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = unequip.Character
	storage, err := s.OpenStorage(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("scroll-storage-%d", a.CharacterID), Kind: "deposit", Slot: 1, TemplateID: old.ItemID, Quantity: 1, ExpectedRevision: storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = stored.Inventory.Character
	loadedStore, err := s.OpenStorage(ctx, a)
	if err != nil || loadedStore.Items[0].Equipment.ScrollLevel != 1 {
		t.Fatalf("storage lost scroll metadata=%+v %v", loadedStore, err)
	}
	withdraw, err := s.ChangeStorage(ctx, domain.StorageRequest{Session: a, OperationID: fmt.Sprintf("scroll-withdraw-%d", a.CharacterID), Kind: "withdraw", StorageItemID: stored.Storage.Items[0].ID, ExpectedRevision: stored.Storage.Revision})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = withdraw.Inventory.Character
	dropped, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: a, ClientTick: 20003, InvType: 1, Source: 1, Destination: 0, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = dropped.Character
	picked, err := s.PickupItem(ctx, domain.ItemPickupRequest{Session: b, ObjectID: dropped.Drop.ObjectID})
	if err != nil {
		t.Fatal(err)
	}
	b.Character = picked.Character
	if eq := equipmentAt(b.Character, 1); eq.ID != old.ID || eq.ScrollLevel != 1 || eq.WATK != old.WATK+2 {
		t.Fatalf("drop lost metadata=%+v", eq)
	}
	trade, err := s.SettleTrade(ctx, domain.TradeSettlement{ID: fmt.Sprintf("scroll-trade-%d", old.ID), First: b, Second: a, FirstOffer: domain.TradeOffer{Items: []domain.TradeItem{{InvType: 1, Slot: 1, Quantity: 1, InstanceID: old.ID, TradeSlot: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = trade.Second.Character
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || equipmentAt(loaded, 1).ScrollLevel != 1 {
		t.Fatalf("trade reload metadata=%+v %v", loaded, err)
	}
	// Cash overlay test setup attaches a purchased-asset serial to the same
	// physical gear; no wallet funding or new equipment is created here.
	var cashID int64
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,state) VALUES($1,$2,$3,1,$4,1,1,1,'inventory') RETURNING id`, a.AccountID, a.CharacterID, a.Lease.WorldID, old.ItemID).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	a.Character, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
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
	for i, toLocker := range []bool{true, false} {
		moved, err := s.MoveCashItem(ctx, domain.CashMoveRequest{Session: cs, OperationID: fmt.Sprintf("scroll-cash-%d-%d", old.ID, i), CashID: cashID, ToLocker: toLocker})
		if err != nil {
			t.Fatal(err)
		}
		cs.Session.Character = moved.Character
		if moved.Item.Equipment.ScrollLevel != 1 {
			t.Fatalf("cash lost scroll metadata=%+v", moved.Item)
		}
	}
	loaded, err = s.GetCharacter(ctx, a.CharacterID)
	if err != nil || equipmentAt(loaded, 1).ScrollLevel != 1 || equipmentAt(loaded, 1).WATK != old.WATK+2 {
		t.Fatalf("cash reload lost stats=%+v %v", loaded, err)
	}
}

func TestScrollFailureWhiteCurseAndOwnerFencing(t *testing.T) {
	ctx, s, sessions := scrollFixture(t)
	a := sessions[0]
	old := equipmentAt(a.Character, -11)
	r := domain.ScrollRequest{Session: a, ClientTick: 21000, SourceSlot: 5, TargetSlot: -11}
	if _, err := s.ApplyScroll(ctx, r); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("wrong scroll category accepted %v", err)
	}
	r.SourceSlot = 3
	r.WhiteFlags = 2
	r.ClientTick++
	failed, err := s.ApplyScroll(ctx, r)
	if err != nil || failed.Status != 0 || equipmentAt(failed.Inventory.Character, -11).UpgradeSlots != old.UpgradeSlots {
		t.Fatalf("white failure=%+v %v", failed, err)
	}
	a.Character = failed.Inventory.Character
	r.Session = a
	r.WhiteFlags = 0
	r.ClientTick++
	failed, err = s.ApplyScroll(ctx, r)
	if err != nil || failed.Status != 0 || equipmentAt(failed.Inventory.Character, -11).UpgradeSlots != old.UpgradeSlots-1 {
		t.Fatalf("ordinary failure=%+v %v", failed, err)
	}
	a.Character = failed.Inventory.Character
	r.Session = a
	r.SourceSlot = 4
	r.ClientTick++
	stale := r
	stale.Session.Generation++
	if _, err = s.ApplyScroll(ctx, stale); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale owner accepted scroll %v", err)
	}
	curse, err := s.ApplyScroll(ctx, r)
	if err != nil || curse.Status != 2 || equipmentAt(curse.Inventory.Character, -11).ItemID != 0 {
		t.Fatalf("curse=%+v %v", curse, err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_equipment WHERE id=$1`, old.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cursed equipment resurrected %d %v", count, err)
	}
	var quantity int
	if err = s.db.QueryRowContext(ctx, `SELECT quantity FROM asset_items WHERE character_id=$1 AND inv_type=2 AND slot=6`, a.CharacterID).Scan(&quantity); err != nil || quantity != 1 {
		t.Fatalf("white scroll consumption=%d %v", quantity, err)
	}
}
