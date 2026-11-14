package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func adminOp(a domain.CharacterSession, name string, r domain.AdminInventoryRequest) domain.AdminInventoryRequest {
	r.OperationID = fmt.Sprintf("admin-test-%s-%d", name, a.CharacterID)
	r.Actor = "admin@example.test"
	r.CharacterID = a.CharacterID
	session := a
	r.Session = &session
	return r
}

func TestAdminInventoryOnline(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	a := actors[0]
	apply := func(name string, r domain.AdminInventoryRequest) domain.AdminInventoryResult {
		t.Helper()
		result, err := s.ApplyAdminInventory(ctx, adminOp(a, name, r))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if result.Character.Revision != a.Character.Revision+1 {
			t.Fatalf("%s: revision %d after %d", name, result.Character.Revision, a.Character.Revision)
		}
		a.Character = result.Character
		return result
	}

	// The fixture starts with a few potions; 650 more top that stack up to
	// 300 and open two new ones.
	held := 0
	for _, item := range a.Character.Items {
		if item.TemplateID == 2000000 {
			held += item.Quantity
		}
	}
	give := domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 2000000, Quantity: 650}
	given := apply("potions", give)
	var stacks []domain.InventoryItem
	for _, item := range given.Character.Items {
		if item.TemplateID == 2000000 {
			stacks = append(stacks, item)
		}
	}
	if len(given.Changes) != 3 || len(stacks) != 3 || stacks[0].Quantity+stacks[1].Quantity+stacks[2].Quantity != held+650 || stacks[0].Quantity != 300 {
		t.Fatalf("potion stacks %+v changes %+v", stacks, given.Changes)
	}
	// The same operation again is a replay of the first outcome.
	replay := adminOp(a, "potions", give)
	replay.Session.Character.Revision-- // what the world still held when it asked again
	again, err := s.ApplyAdminInventory(ctx, replay)
	if err != nil || !again.Replayed || len(again.Changes) != 3 || again.Character.Revision != a.Character.Revision {
		t.Fatalf("replay %+v %v", again, err)
	}
	other := adminOp(a, "potions", domain.AdminInventoryRequest{Kind: domain.AdminGiveMesos, Mesos: 1})
	if _, err = s.ApplyAdminInventory(ctx, other); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("an operation ID was reused for another change: %v", err)
	}

	set := apply("quantity", domain.AdminInventoryRequest{Kind: domain.AdminSetQuantity, InvType: 2, AssetID: stacks[2].ID, Quantity: 7})
	if len(set.Changes) != 1 || set.Changes[0].Mode != 1 || set.Changes[0].Quantity != 7 || set.Changes[0].Slot != stacks[2].Slot {
		t.Fatalf("quantity change %+v", set.Changes)
	}
	for name, quantity := range map[string]int{"zero": 0, "over": 301} { // the template's limit is 300
		bad := adminOp(a, "quantity-"+name, domain.AdminInventoryRequest{Kind: domain.AdminSetQuantity, InvType: 2, AssetID: stacks[2].ID, Quantity: quantity})
		if _, err = s.ApplyAdminInventory(ctx, bad); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("quantity %d accepted: %v", quantity, err)
		}
	}

	swords := apply("swords", domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 1302000, Quantity: 2})
	if len(swords.Changes) != 2 || swords.Changes[0].Equipment == nil || swords.Changes[0].Equipment.WATK != 17 || swords.Changes[0].Slot == swords.Changes[1].Slot {
		t.Fatalf("sword changes %+v", swords.Changes)
	}
	sword := *swords.Changes[0].Equipment
	stats := sword
	stats.WATK, stats.STR, stats.UpgradeSlots, stats.ScrollLevel = 999, 30000, 3, 4
	stats.ItemID, stats.Slot = 1, 99 // identity never comes from the request
	edited := apply("sword-stats", domain.AdminInventoryRequest{Kind: domain.AdminSetEquipment, InvType: 1, AssetID: sword.ID, Equipment: &stats})
	if edited.Worn || len(edited.Changes) != 2 || edited.Changes[0].Mode != 3 || edited.Changes[1].Mode != 0 || edited.Changes[1].Slot != sword.Slot {
		t.Fatalf("stat changes %+v", edited)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range loaded.Equipment {
		if e.ID == sword.ID {
			found = e.ItemID == 1302000 && e.Slot == sword.Slot && e.WATK == 999 && e.STR == 30000 && e.UpgradeSlots == 3 && e.ScrollLevel == 4
		}
	}
	if !found {
		t.Fatalf("stored sword %+v", loaded.Equipment)
	}
	tooHigh := sword
	tooHigh.WATK = domain.AdminEquipmentStatMax + 1
	if _, err = s.ApplyAdminInventory(ctx, adminOp(a, "sword-over", domain.AdminInventoryRequest{Kind: domain.AdminSetEquipment, InvType: 1, AssetID: sword.ID, Equipment: &tooHigh})); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("stat above the client's range accepted: %v", err)
	}

	// Worn equipment: more HP on the weapon raises the character's maximum.
	var wornWeapon domain.Equipment
	for _, e := range a.Character.Equipment {
		if e.Slot == -11 {
			wornWeapon = e
		}
	}
	if wornWeapon.ID == 0 {
		t.Fatal("fixture has no worn weapon")
	}
	if a.Character.VitalsModel == 1 {
		maxHP := a.Character.MaxHP
		wornWeapon.HP += 100
		wornResult := apply("worn-hp", domain.AdminInventoryRequest{Kind: domain.AdminSetEquipment, InvType: 1, AssetID: wornWeapon.ID, Equipment: &wornWeapon})
		if !wornResult.Worn || wornResult.Character.MaxHP != maxHP+100 || wornResult.Changes[1].Slot != -11 {
			t.Fatalf("worn result max HP %d (was %d) %+v", wornResult.Character.MaxHP, maxHP, wornResult.Changes)
		}
	}

	removed := apply("remove-stack", domain.AdminInventoryRequest{Kind: domain.AdminRemove, InvType: 2, AssetID: stacks[0].ID})
	if len(removed.Changes) != 1 || removed.Changes[0].Mode != 3 || removed.Changes[0].Slot != stacks[0].Slot {
		t.Fatalf("remove change %+v", removed.Changes)
	}
	apply("remove-sword", domain.AdminInventoryRequest{Kind: domain.AdminRemove, InvType: 1, AssetID: sword.ID})
	if _, err = s.ApplyAdminInventory(ctx, adminOp(a, "remove-gone", domain.AdminInventoryRequest{Kind: domain.AdminRemove, InvType: 1, AssetID: sword.ID})); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("removing a missing item: %v", err)
	}

	// Mesos beyond 32 bits, with the 64-bit balance switch on.
	defer domain.SetLongMoneyForTest(true)()
	before := a.Character.Mesos
	rich := apply("mesos", domain.AdminInventoryRequest{Kind: domain.AdminGiveMesos, Mesos: 5_000_000_000})
	if rich.Character.Mesos != before+5_000_000_000 {
		t.Fatalf("mesos %d", rich.Character.Mesos)
	}
	var ledger int64
	if err = s.db.QueryRowContext(ctx, `SELECT balance_after FROM asset_meso_ledger WHERE character_id=$1 AND reason='web_admin'`, a.CharacterID).Scan(&ledger); err != nil || ledger != rich.Character.Mesos {
		t.Fatalf("ledger %d %v", ledger, err)
	}
	if _, err = s.ApplyAdminInventory(ctx, adminOp(a, "mesos-over", domain.AdminInventoryRequest{Kind: domain.AdminGiveMesos, Mesos: -rich.Character.Mesos - 1})); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("took more mesos than held: %v", err)
	}
	apply("mesos-take", domain.AdminInventoryRequest{Kind: domain.AdminGiveMesos, Mesos: -5_000_000_000})

	// A world holding an older copy must not have its change applied.
	stale := adminOp(a, "stale", domain.AdminInventoryRequest{Kind: domain.AdminGiveMesos, Mesos: 1})
	stale.Session.Character.Revision--
	if _, err = s.ApplyAdminInventory(ctx, stale); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale world copy accepted: %v", err)
	}
	var audited int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM ops_admin_inventory_ops WHERE character_id=$1 AND online AND actor='admin@example.test'`, a.CharacterID).Scan(&audited); err != nil || audited < 8 {
		t.Fatalf("audit rows %d %v", audited, err)
	}
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM ops_admin_inventory_ops WHERE character_id=$1`, a.CharacterID)
	})
}

func TestAdminInventoryOfflineAndWallet(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	a := actors[0]
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM ops_admin_inventory_ops WHERE account_id=$1`, a.AccountID)
	})
	offline := func(name string, r domain.AdminInventoryRequest) domain.AdminInventoryRequest {
		r = adminOp(a, name, r)
		r.Session = nil
		return r
	}
	// While a world holds the character, only that world may change it.
	if _, err := s.ApplyAdminInventory(ctx, offline("held", domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 2000000, Quantity: 1})); !errors.Is(err, domain.ErrAlreadyOnline) {
		t.Fatalf("offline edit of a held character: %v", err)
	}
	if err := s.ReleaseCharacter(ctx, a); err != nil {
		t.Fatal(err)
	}
	released, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ApplyAdminInventory(ctx, offline("offline", domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 1302000, Quantity: 1}))
	if err != nil || len(result.Changes) != 1 || result.Character.Revision != released.Revision+1 {
		t.Fatalf("offline give %+v %v", result, err)
	}
	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || len(loaded.Equipment) != len(released.Equipment)+1 {
		t.Fatalf("stored equipment %+v %v", loaded.Equipment, err)
	}
	// A session that says the character is in a live world is enough to
	// refuse, even before that world has recorded its ownership.
	var prior struct {
		state, instance  string
		character, epoch int64
		world            int
	}
	if err = s.db.QueryRowContext(ctx, `SELECT state,character_id,world_id,owner_instance,world_epoch FROM ops_account_sessions WHERE account_id=$1`, a.AccountID).Scan(&prior.state, &prior.character, &prior.world, &prior.instance, &prior.epoch); err != nil {
		t.Fatal(err)
	}
	entering, err := s.db.ExecContext(ctx, `UPDATE ops_account_sessions a SET state='world',character_id=$1,world_id=l.world_id,owner_instance=l.instance_id,world_epoch=l.epoch
 FROM ops_runtime_leases l WHERE l.world_id=124 AND l.expires_at>clock_timestamp() AND a.account_id=$2`, a.CharacterID, a.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := entering.RowsAffected(); n != 1 {
		t.Fatalf("session rows marked as entering: %d", n)
	}
	if _, err = s.ApplyAdminInventory(ctx, offline("entering", domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 2000000, Quantity: 1})); !errors.Is(err, domain.ErrAlreadyOnline) {
		t.Fatalf("offline edit of a character entering a world: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state=$1,character_id=$2,world_id=$3,owner_instance=$4,world_epoch=$5 WHERE account_id=$6`, prior.state, prior.character, prior.world, prior.instance, prior.epoch, a.AccountID); err != nil {
		t.Fatal(err)
	}

	missing := offline("missing-character", domain.AdminInventoryRequest{Kind: domain.AdminGiveMesos, Mesos: 1})
	missing.CharacterID = 1 << 40
	if _, err = s.ApplyAdminInventory(ctx, missing); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing character: %v", err)
	}
	if _, err = s.ApplyAdminInventory(ctx, offline("unknown-item", domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 1999999, Quantity: 1})); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}

	defer domain.SetLongMoneyForTest(true)()
	wallet := domain.AdminWalletRequest{OperationID: fmt.Sprintf("admin-test-wallet-%d", a.AccountID), Actor: "admin@example.test", AccountID: a.AccountID, CharacterID: a.CharacterID, Currency: domain.CashCurrencyCredit, Delta: 3_000_000_000}
	balance, err := s.AdjustAdminWallet(ctx, wallet)
	if err != nil || balance.Credit != 3_000_000_000 || balance.Points != 0 {
		t.Fatalf("wallet %+v %v", balance, err)
	}
	if balance, err = s.AdjustAdminWallet(ctx, wallet); err != nil || balance.Credit != 3_000_000_000 {
		t.Fatalf("wallet replay %+v %v", balance, err)
	}
	points := wallet
	points.OperationID += "-points"
	points.Currency, points.Delta = domain.CashCurrencyPoints, 77
	if balance, err = s.AdjustAdminWallet(ctx, points); err != nil || balance.Credit != 3_000_000_000 || balance.Points != 77 {
		t.Fatalf("points %+v %v", balance, err)
	}
	take := points
	take.OperationID += "-take"
	take.Delta = -78
	if _, err = s.AdjustAdminWallet(ctx, take); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("took more points than held: %v", err)
	}
	if read, err := s.ReadAdminWallet(ctx, a.AccountID); err != nil || read != balance {
		t.Fatalf("read wallet %+v %v", read, err)
	}
	var ledger int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_cash_ledger WHERE account_id=$1`, a.AccountID).Scan(&ledger); err != nil || ledger != 2 {
		t.Fatalf("cash ledger rows %d %v", ledger, err)
	}
}

// An edit made from a page that no longer shows what the character holds is
// refused whole: nothing is overwritten, duplicated or removed.
func TestAdminInventoryRefusesStaleView(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	a := actors[0]
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM ops_admin_inventory_ops WHERE character_id=$1`, a.CharacterID)
	})
	try := func(name string, r domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		result, err := s.ApplyAdminInventory(ctx, adminOp(a, name, r))
		if err == nil {
			a.Character = result.Character
		}
		return result, err
	}
	given, err := try("view-sword", domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 1302000, Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	sword := *given.Changes[0].Equipment
	seen := sword // what the page shows from now on
	// The player scrolls the sword after the page was drawn.
	scrolled := sword
	scrolled.WATK, scrolled.UpgradeSlots, scrolled.ScrollLevel = sword.WATK+2, sword.UpgradeSlots-1, 1
	if _, err = try("view-scroll", domain.AdminInventoryRequest{Kind: domain.AdminSetEquipment, InvType: 1, AssetID: sword.ID, Equipment: &scrolled,
		Expect: &domain.AdminExpect{ItemID: 1302000, Equipment: &sword}}); err != nil {
		t.Fatal(err)
	}
	revision := a.Character.Revision
	wanted := seen
	wanted.STR = 50
	stale := &domain.AdminExpect{ItemID: 1302000, Equipment: &seen}
	if _, err = try("view-stale-stats", domain.AdminInventoryRequest{Kind: domain.AdminSetEquipment, InvType: 1, AssetID: sword.ID, Equipment: &wanted, Expect: stale}); !errors.Is(err, domain.ErrAdminChanged) {
		t.Fatalf("stats written over a scrolled sword: %v", err)
	}
	if _, err = try("view-stale-remove", domain.AdminInventoryRequest{Kind: domain.AdminRemove, InvType: 1, AssetID: sword.ID, Expect: stale}); !errors.Is(err, domain.ErrAdminChanged) {
		t.Fatalf("removed a sword that changed: %v", err)
	}
	if _, err = try("view-other-item", domain.AdminInventoryRequest{Kind: domain.AdminRemove, InvType: 1, AssetID: sword.ID,
		Expect: &domain.AdminExpect{ItemID: 1302001, Equipment: &scrolled}}); !errors.Is(err, domain.ErrAdminChanged) {
		t.Fatalf("removed a different item than the one seen: %v", err)
	}

	potions, err := try("view-potions", domain.AdminInventoryRequest{Kind: domain.AdminGiveItem, ItemID: 2000000, Quantity: 40})
	if err != nil {
		t.Fatal(err)
	}
	revision = a.Character.Revision
	var stack domain.InventoryItem
	for _, item := range potions.Character.Items {
		if item.TemplateID == 2000000 {
			stack = item
		}
	}
	// The page still shows the count from before the player drank some.
	old := &domain.AdminExpect{ItemID: 2000000, Quantity: stack.Quantity + 5}
	if _, err = try("view-stale-count", domain.AdminInventoryRequest{Kind: domain.AdminSetQuantity, InvType: 2, AssetID: stack.ID, Quantity: 90, Expect: old}); !errors.Is(err, domain.ErrAdminChanged) {
		t.Fatalf("count written over a changed stack: %v", err)
	}
	if _, err = try("view-stale-stack-remove", domain.AdminInventoryRequest{Kind: domain.AdminRemove, InvType: 2, AssetID: stack.ID, Expect: old}); !errors.Is(err, domain.ErrAdminChanged) {
		t.Fatalf("removed a changed stack: %v", err)
	}

	loaded, err := s.GetCharacter(ctx, a.CharacterID)
	if err != nil || loaded.Revision != revision {
		t.Fatalf("refused edits moved the revision %d -> %d %v", revision, loaded.Revision, err)
	}
	kept := false
	for _, e := range loaded.Equipment {
		if e.ID == sword.ID {
			kept = e.WATK == scrolled.WATK && e.STR == sword.STR && e.UpgradeSlots == scrolled.UpgradeSlots && e.ScrollLevel == 1
		}
	}
	held := 0
	for _, item := range loaded.Items {
		if item.ID == stack.ID {
			held = item.Quantity
		}
	}
	if !kept || held != stack.Quantity {
		t.Fatalf("refused edits changed something: sword kept %v, potions %d of %d", kept, held, stack.Quantity)
	}
	var refused int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM ops_admin_inventory_ops WHERE character_id=$1 AND operation_id LIKE '%view-stale%'`, a.CharacterID).Scan(&refused); err != nil || refused != 0 {
		t.Fatalf("refused edits were recorded as done: %d %v", refused, err)
	}

	// With the current view the same edits go through.
	current := &domain.AdminExpect{ItemID: 1302000, Equipment: &scrolled}
	wanted = scrolled
	wanted.STR = 50
	if _, err = try("view-fresh-stats", domain.AdminInventoryRequest{Kind: domain.AdminSetEquipment, InvType: 1, AssetID: sword.ID, Equipment: &wanted, Expect: current}); err != nil {
		t.Fatal(err)
	}
	if _, err = try("view-fresh-count", domain.AdminInventoryRequest{Kind: domain.AdminSetQuantity, InvType: 2, AssetID: stack.ID, Quantity: 90,
		Expect: &domain.AdminExpect{ItemID: 2000000, Quantity: stack.Quantity}}); err != nil {
		t.Fatal(err)
	}
}
