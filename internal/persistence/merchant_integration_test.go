package persistence

import (
	"context"
	"errors"
	"os"
	"testing"

	"maple-go-server/internal/domain"
)

func merchantFixture(t *testing.T) (context.Context, *Store, domain.CharacterSession, domain.CharacterSession) {
	t.Helper()
	wzRoot := os.Getenv("MAPLEGO_WZ_ROOT")
	usePrivateSchema(t, "merchant")
	t.Setenv("MAPLEGO_WZ_ROOT", wzRoot)
	t.Setenv("MAPLEGO_REDIS_ADDR", "")
	t.Setenv("MAPLEGO_REDIS_PASSWORD", "")
	ctx, s, actors := inventoryFixture(t)
	owner, buyer := actors[0], actors[1]
	t.Cleanup(func() {
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM asset_merchant_shops WHERE account_id IN($1,$2)`, actors[0].AccountID, actors[1].AccountID)
	})
	// A hired merchant permit in the cash inventory.
	var cashID int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,state) VALUES($1,$2,$3,0,5030000,1,5,1,'inventory') RETURNING id`, owner.AccountID, owner.CharacterID, owner.Lease.WorldID).Scan(&cashID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,5,1,5030000,1)`, owner.CharacterID); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []*domain.CharacterSession{&owner, &buyer} {
		c, err := s.GetCharacter(ctx, actor.CharacterID)
		if err != nil {
			t.Fatal(err)
		}
		c.MapID, c.X, c.Y = 910000001, 100, 34
		actor.Character = c
	}
	return ctx, s, owner, buyer
}

func merchantPotions(c domain.Character) (slot, quantity int) {
	for _, item := range c.Items {
		if item.InvType == 2 {
			return item.Slot, item.Quantity
		}
	}
	return 0, 0
}

func merchantDo(t *testing.T, ctx context.Context, s *Store, session *domain.CharacterSession, shop *domain.MerchantShop, r domain.MerchantRequest) (domain.MerchantResult, error) {
	t.Helper()
	r.Session, r.ShopID, r.ExpectedRevision = *session, shop.ID, shop.Revision
	result, err := s.ChangeMerchant(ctx, r)
	if err == nil {
		session.Character = result.Inventory.Character
		*shop = result.Shop
	}
	return result, err
}

func TestMerchantSellBuyCollectReclaim(t *testing.T) {
	ctx, s, owner, buyer := merchantFixture(t)
	potionSlot, potions := merchantPotions(owner.Character)
	if potions != 5 || owner.Character.Mesos != 1000 || buyer.Character.Mesos != 1000 {
		t.Fatalf("fixture: potions=%d owner=%d buyer=%d", potions, owner.Character.Mesos, buyer.Character.Mesos)
	}
	if _, err := s.CreateMerchant(ctx, domain.MerchantCreate{Session: owner, PermitSlot: 2, PermitItemID: 5030000, Title: "shop"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("permit in another slot: %v", err)
	}
	if _, err := s.CreateMerchant(ctx, domain.MerchantCreate{Session: buyer, PermitSlot: 1, PermitItemID: 5030000, Title: "shop"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no permit: %v", err)
	}
	shop, err := s.CreateMerchant(ctx, domain.MerchantCreate{Session: owner, PermitSlot: 1, PermitItemID: 5030000, Title: "精靈小舖", Foothold: 3})
	if err != nil || shop.State != domain.MerchantSetup || shop.MapID != 910000001 || shop.X != 100 || shop.ChannelID != owner.ChannelID || shop.OwnerName != owner.Character.Name {
		t.Fatalf("create: %+v %v", shop, err)
	}
	if _, err = s.CreateMerchant(ctx, domain.MerchantCreate{Session: owner, PermitSlot: 1, PermitItemID: 5030000, Title: "again"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("second shop: %v", err)
	}

	// Move the weapon into the bag so an equipment row is covered too.
	weapon := equipmentAt(owner.Character, -11)
	moved, err := s.MoveInventory(ctx, domain.InventoryMoveRequest{Session: owner, ClientTick: 1, InvType: 1, Source: -11, Destination: 1, Quantity: -1})
	if err != nil {
		t.Fatal(err)
	}
	owner.Character = moved.Character

	put, err := merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 2, Slot: potionSlot, Bundles: 2, PerBundle: 2, Price: 300})
	if err != nil || len(shop.Items) != 1 || shop.Items[0].Bundles != 2 || shop.Items[0].PerBundle != 2 || len(put.Inventory.Changes) != 1 {
		t.Fatalf("put potions: %+v %v", put, err)
	}
	if _, left := merchantPotions(owner.Character); left != 1 {
		t.Fatalf("potions left with owner: %d", left)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 1, Slot: 1, Bundles: 1, PerBundle: 1, Price: 500}); err != nil || len(shop.Items) != 2 || shop.Items[1].Equipment == nil || shop.Items[1].Equipment.ID != weapon.ID {
		t.Fatalf("put weapon: %+v %v", shop.Items, err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 5, Slot: 1, Bundles: 1, PerBundle: 1, Price: 1}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cash item on sale with the setting off: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 1, Price: 300}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("buy before opening: %v", err)
	}

	opened, found, err := s.SetMerchantState(ctx, shop.ID, []string{domain.MerchantSetup}, domain.MerchantOpen)
	if err != nil || !found || opened.State != domain.MerchantOpen || len(opened.Items) != 2 {
		t.Fatalf("open: %+v %v", opened, err)
	}
	shop = opened
	active, err := s.ActiveMerchants(ctx, owner.Lease.WorldID)
	if err != nil || len(active) != 1 || active[0].ID != shop.ID || len(active[0].Items) != 2 {
		t.Fatalf("active after open: %+v %v", active, err)
	}

	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 1, Price: 300}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner buying from own shop: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "take", ItemID: shop.Items[0].ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("taking stock while open: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 1, Price: 299}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("price the buyer was not shown: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 3, Price: 300}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("more lots than stocked: %v", err)
	}
	bought, err := merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 1, Price: 300})
	if err != nil || buyer.Character.Mesos != 700 || shop.Mesos != 300 || shop.Items[0].Bundles != 1 || bought.Sale == nil || bought.Sale.Total != 300 || bought.Sale.Buyer != buyer.Character.Name {
		t.Fatalf("buy potions: %+v %v", bought, err)
	}
	if _, have := merchantPotions(buyer.Character); have != 7 {
		t.Fatalf("buyer potions: %d", have)
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[1].ID, Bundles: 1, Price: 500}); err != nil || buyer.Character.Mesos != 200 || shop.Mesos != 800 || shop.Items[1].Bundles != 0 {
		t.Fatalf("buy weapon: %+v %v", shop, err)
	}
	owned := false
	for _, e := range buyer.Character.Equipment {
		owned = owned || e.ID == weapon.ID && e.Slot > 0
	}
	if !owned {
		t.Fatal("the weapon did not reach the buyer's bag")
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 1, Price: 300}); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("buy without mesos: %v", err)
	}

	// The owner is offline for all of the above: the income sits on the shop.
	stored, found, err := s.MerchantFor(ctx, owner.AccountID, owner.Lease.WorldID)
	if err != nil || !found || stored.Mesos != 800 || len(stored.Sales) != 2 || stored.Revision != shop.Revision {
		t.Fatalf("stored shop: %+v %v", stored, err)
	}
	var ledger int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_meso_ledger WHERE character_id=$1 AND reason='merchant_buy'`, buyer.CharacterID).Scan(&ledger); err != nil || ledger != 2 {
		t.Fatalf("buyer ledger rows: %d %v", ledger, err)
	}

	maintained, _, err := s.SetMerchantState(ctx, shop.ID, []string{domain.MerchantOpen}, domain.MerchantMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	shop = maintained
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 1, Price: 300}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("buy during maintenance: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "collect"}); err != nil || owner.Character.Mesos != 1800 || shop.Mesos != 0 || len(shop.Items) != 1 || shop.Items[0].Position != 1 {
		t.Fatalf("collect: owner=%d shop=%+v %v", owner.Character.Mesos, shop, err)
	}
	reclaimed, err := merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "reclaim"})
	if err != nil || !reclaimed.Deleted {
		t.Fatalf("reclaim: %+v %v", reclaimed, err)
	}
	if _, have := merchantPotions(owner.Character); have != 3 {
		t.Fatalf("owner potions after reclaim: %d", have)
	}
	if _, found, err = s.MerchantFor(ctx, owner.AccountID, owner.Lease.WorldID); err != nil || found {
		t.Fatalf("shop row after reclaim: found=%v %v", found, err)
	}
}

func TestMerchantClosedShopWaitsAtFredrick(t *testing.T) {
	ctx, s, owner, buyer := merchantFixture(t)
	potionSlot, _ := merchantPotions(owner.Character)
	shop, err := s.CreateMerchant(ctx, domain.MerchantCreate{Session: owner, PermitSlot: 1, PermitItemID: 5030000, Title: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	// An empty shop that closes leaves no package behind.
	if _, found, err := s.SetMerchantState(ctx, shop.ID, []string{domain.MerchantSetup}, domain.MerchantClosed); err != nil || found {
		t.Fatalf("empty close: found=%v %v", found, err)
	}
	if shop, err = s.CreateMerchant(ctx, domain.MerchantCreate{Session: owner, PermitSlot: 1, PermitItemID: 5030000, Title: "shop"}); err != nil {
		t.Fatal(err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 2, Slot: potionSlot, Bundles: 5, PerBundle: 1, Price: 100}); err != nil {
		t.Fatal(err)
	}
	if shop, _, err = s.SetMerchantState(ctx, shop.ID, []string{domain.MerchantSetup}, domain.MerchantOpen); err != nil {
		t.Fatal(err)
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 2, Price: 100}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SetMerchantState(ctx, shop.ID, []string{domain.MerchantMaintenance}, domain.MerchantClosed); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("close from a state the shop is not in: %v", err)
	}
	closed, found, err := s.SetMerchantState(ctx, shop.ID, []string{domain.MerchantOpen}, domain.MerchantClosed)
	if err != nil || !found || closed.State != domain.MerchantClosed || closed.Mesos != 200 || len(closed.Items) != 1 || closed.Items[0].Bundles != 3 {
		t.Fatalf("close to Fredrick: %+v %v", closed, err)
	}
	shop = closed
	if active, err := s.ActiveMerchants(ctx, owner.Lease.WorldID); err != nil || len(active) != 0 {
		t.Fatalf("closed shop still active: %+v %v", active, err)
	}
	if _, err = s.CreateMerchant(ctx, domain.MerchantCreate{Session: owner, PermitSlot: 1, PermitItemID: 5030000, Title: "new"}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("new shop while a package waits: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "reclaim"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("someone else's package: %v", err)
	}

	// Income is a 64-bit balance. A payout the owner cannot hold is refused
	// whole, and nothing leaves the package.
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_merchant_shops SET mesos=5000000000 WHERE id=$1`, shop.ID); err != nil {
		t.Fatal(err)
	}
	rich, _, err := s.MerchantFor(ctx, owner.AccountID, owner.Lease.WorldID)
	if err != nil || rich.Mesos != 5_000_000_000 {
		t.Fatalf("64-bit income: %+v %v", rich, err)
	}
	restore := domain.SetLongMoneyForTest(false)
	_, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "reclaim"})
	restore()
	if !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("payout above the 32-bit balance cap: %v", err)
	}
	if kept, found, err := s.MerchantFor(ctx, owner.AccountID, owner.Lease.WorldID); err != nil || !found || kept.Mesos != 5_000_000_000 || len(kept.Items) != 1 {
		t.Fatalf("package after refused payout: %+v %v", kept, err)
	}
	// Under long_money the owner takes all of it in one go.
	defer domain.SetLongMoneyForTest(true)()
	result, err := merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "reclaim"})
	if err != nil || !result.Deleted || owner.Character.Mesos != 5_000_001_000 {
		t.Fatalf("Fredrick reclaim: %+v %v", result, err)
	}
	if _, have := merchantPotions(owner.Character); have != 3 {
		t.Fatalf("potions back from Fredrick: %d", have)
	}
	if _, found, err = s.MerchantFor(ctx, owner.AccountID, owner.Lease.WorldID); err != nil || found {
		t.Fatalf("package after reclaim: found=%v %v", found, err)
	}
}

func TestMerchantWidePricesAndCashItems(t *testing.T) {
	ctx, s, owner, buyer := merchantFixture(t)
	defer domain.SetLongMoneyForTest(true)()
	potionSlot, _ := merchantPotions(owner.Character)
	// A megaphone stack in the owner's cash inventory.
	var megaphone int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,character_id,world_id,sn,template_id,quantity,inv_type,slot,state) VALUES($1,$2,$3,0,5072000,3,5,2,'inventory') RETURNING id`, owner.AccountID, owner.CharacterID, owner.Lease.WorldID).Scan(&megaphone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,5,2,5072000,3)`, owner.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=20000000000 WHERE id=$1`, buyer.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	if owner.Character, err = s.GetCharacter(ctx, owner.CharacterID); err != nil {
		t.Fatal(err)
	}
	if buyer.Character, err = s.GetCharacter(ctx, buyer.CharacterID); err != nil || buyer.Character.Mesos != 20_000_000_000 {
		t.Fatalf("rich buyer: %d %v", buyer.Character.Mesos, err)
	}
	owner.Character.MapID, owner.Character.X, owner.Character.Y = 910000001, 100, 34
	shop, err := s.CreateMerchant(ctx, domain.MerchantCreate{Session: owner, PermitSlot: 1, PermitItemID: 5030000, Title: "shop"})
	if err != nil {
		t.Fatal(err)
	}

	// Prices: 32-bit without long money, 64-bit with it, and a row whose
	// full purchase would not fit in 64 bits is refused when it is listed.
	restore := domain.SetLongMoneyForTest(false)
	_, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 2, Slot: potionSlot, Bundles: 2, PerBundle: 1, Price: 3_000_000_000})
	restore()
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("a 64-bit price without long money: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 2, Slot: potionSlot, Bundles: 2, PerBundle: 1, Price: 9_000_000_000_000_000_000}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("a row total above int64: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 2, Slot: potionSlot, Bundles: 2, PerBundle: 1, Price: 6_000_000_000}); err != nil || shop.Items[0].Price != 6_000_000_000 {
		t.Fatalf("wide price: %+v %v", shop.Items, err)
	}

	// Cash items: off by default; on, only a whole serial, never the permit.
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 5, Slot: 2, Bundles: 1, PerBundle: 3, Price: 100}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cash item with the setting off: %v", err)
	}
	defer domain.SetMerchantCashItemsForTest(true)()
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 5, Slot: 1, Bundles: 1, PerBundle: 1, Price: 100}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("selling the shop permit: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 5, Slot: 2, Bundles: 1, PerBundle: 2, Price: 100}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("splitting a cash serial: %v", err)
	}
	if _, err = merchantDo(t, ctx, s, &owner, &shop, domain.MerchantRequest{Kind: "put", InvType: 5, Slot: 2, Bundles: 1, PerBundle: 3, Price: 100}); err != nil || len(shop.Items) != 2 || shop.Items[1].Item.CashID != megaphone {
		t.Fatalf("cash item on sale: %+v %v", shop.Items, err)
	}
	cashState := func() (state string, character, account int64) {
		var who *int64
		if err := s.db.QueryRowContext(ctx, `SELECT state,character_id,account_id FROM asset_cash_items WHERE id=$1`, megaphone).Scan(&state, &who, &account); err != nil {
			t.Fatal(err)
		}
		if who != nil {
			character = *who
		}
		return
	}
	if state, who, _ := cashState(); state != "merchant" || who != 0 {
		t.Fatalf("serial on sale: %s %d", state, who)
	}
	for _, item := range owner.Character.Items {
		if item.TemplateID == 5072000 {
			t.Fatal("the cash item is still in the owner's bag")
		}
	}

	if shop, _, err = s.SetMerchantState(ctx, shop.ID, []string{domain.MerchantSetup}, domain.MerchantOpen); err != nil {
		t.Fatal(err)
	}
	if shop.Items[1].Item.CashID != megaphone {
		t.Fatal("the serial was lost on reload")
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[0].ID, Bundles: 2, Price: 6_000_000_000}); err != nil || buyer.Character.Mesos != 8_000_000_000 || shop.Mesos != 11_640_000_000 { // 3% seller fee
		t.Fatalf("wide purchase: buyer=%d income=%d %v", buyer.Character.Mesos, shop.Mesos, err)
	}
	if _, err = merchantDo(t, ctx, s, &buyer, &shop, domain.MerchantRequest{Kind: "buy", ItemID: shop.Items[1].ID, Bundles: 1, Price: 100}); err != nil {
		t.Fatalf("buy cash item: %v", err)
	}
	if state, who, account := cashState(); state != "inventory" || who != buyer.CharacterID || account != buyer.AccountID {
		t.Fatalf("serial after purchase: %s %d %d", state, who, account)
	}
	reloaded, err := s.GetCharacter(ctx, buyer.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, item := range reloaded.Items {
		held = held || item.TemplateID == 5072000 && item.CashID == megaphone && item.Quantity == 3
	}
	if !held {
		t.Fatalf("the buyer does not hold the cash item: %+v", reloaded.Items)
	}
}

func TestMerchantTaxMatchesTheNativeTable(t *testing.T) {
	for total, want := range map[int64]int64{
		99_999:                    0,
		100_000:                   400,
		999_999:                   4_000,
		1_000_000:                 9_000,
		5_000_000:                 75_000,
		10_000_000:                200_000,
		25_000_000:                625_000,
		100_000_000:               3_000_000,
		12_000_000_000:            360_000_000,
		9_000_000_000_000_000_000: 270_000_000_000_000_000,
	} {
		if got := MerchantTax(total); got != want {
			t.Errorf("MerchantTax(%d) = %d, want %d", total, got, want)
		}
	}
}
