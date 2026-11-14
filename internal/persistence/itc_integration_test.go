package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func itcSessions(t *testing.T) (context.Context, *Store, []domain.CashSession) {
	t.Helper()
	ctx, s, sessions := inventoryFixture(t)
	lease := cashTestLease(t, ctx, s, sessions[0].CharacterID)
	out := make([]domain.CashSession, 0, 2)
	for i, a := range sessions {
		id := a.CharacterID
		t.Cleanup(func() {
			cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_itc_cart WHERE character_id=$1 OR item_id IN(SELECT id FROM asset_itc_items WHERE seller_id=$1 OR owner_id=$1)`, id)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM asset_itc_items WHERE seller_id=$1 OR owner_id=$1`, id)
			_, _ = s.db.ExecContext(cleanup, `DELETE FROM ops_itc_operations WHERE character_id=$1`, id)
		})
		handle := a.CharacterID + 300000 + int64(i)
		if _, err := s.BeginITCTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		cs, err := s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
		if err != nil || !cs.ITC {
			t.Fatalf("itc claim %+v %v", cs, err)
		}
		out = append(out, cs)
	}
	return ctx, s, out
}

func TestITCSellBuyTakeAndCancel(t *testing.T) {
	ctx, s, sessions := itcSessions(t)
	seller, buyer := sessions[0], sessions[1]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=20000 WHERE id=$1`, seller.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	for _, cs := range sessions {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_cash_wallets(account_id,points) VALUES($1,5000) ON CONFLICT(account_id) DO UPDATE SET points=5000`, cs.Session.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	c, err := readCharacterSQL(ctx, s.db, seller.Session.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	var item domain.InventoryItem
	for _, it := range c.Items {
		if it.InvType >= 2 && it.InvType <= 4 && it.CashID == 0 && it.ExpiresAt == 0 {
			item = it
			break
		}
	}
	if item.ID == 0 {
		// The starter kit has no bag item: give one.
		if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,2,1,2000000,10)`, c.ID); err != nil {
			t.Fatal(err)
		}
		item = domain.InventoryItem{InvType: 2, Slot: 1, TemplateID: 2000000, Quantity: 10}
	}
	seq := 0
	op := func(cs domain.CashSession, kind string, listing int64, mutate func(*domain.ITCRequest)) (domain.ITCResult, error) {
		seq++
		r := domain.ITCRequest{Session: cs, Kind: kind, ListingID: listing, OperationID: fmt.Sprintf("itc-test-%d-%d-%d", cs.Session.CharacterID, time.Now().UnixNano(), seq)}
		if mutate != nil {
			mutate(&r)
		}
		return s.ChangeITC(ctx, r)
	}
	register := func(r *domain.ITCRequest) {
		r.InvType, r.Slot, r.TemplateID, r.Quantity, r.Price = item.InvType, item.Slot, item.TemplateID, 1, 1000
	}
	if _, err = op(seller, "register", 0, func(r *domain.ITCRequest) { register(r); r.Price = 19 }); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("price below 20 accepted: %v", err)
	}
	res, err := op(seller, "register", 0, register)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inventory.Character.Mesos != 20000-domain.ITCRegistrationFee || len(res.Inventory.Changes) != 1 {
		t.Fatalf("fee or inventory change missing: %+v", res.Inventory)
	}
	page, err := s.ITCList(ctx, buyer, domain.ITCView{Tab: 1})
	if err != nil {
		t.Fatal(err)
	}
	var listing domain.ITCListing
	for _, l := range page.Items {
		if l.SellerID == seller.Session.CharacterID {
			listing = l
		}
	}
	if listing.ID == 0 || listing.Price != 1000 || listing.Item.TemplateID != item.TemplateID || listing.Item.Quantity != 1 {
		t.Fatalf("listing not on the 販售 list: %+v", page)
	}
	if _, err = op(seller, "buy", listing.ID, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("seller bought own listing: %v", err)
	}
	if _, err = op(buyer, "cart_add", listing.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = op(buyer, "cart_add", listing.ID, nil); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate cart row: %v", err)
	}
	if cart, err := s.ITCList(ctx, buyer, domain.ITCView{Tab: 4}); err != nil || cart.Total != 1 {
		t.Fatalf("cart %+v %v", cart, err)
	}
	res, err = op(buyer, "buy", listing.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(5000 - 1000 - domain.ITCCommission(1000)); res.Wallet.Points != want {
		t.Fatalf("buyer points %d, want %d", res.Wallet.Points, want)
	}
	var sellerPoints int64
	if err = s.db.QueryRowContext(ctx, `SELECT points FROM asset_cash_wallets WHERE account_id=$1`, seller.Session.AccountID).Scan(&sellerPoints); err != nil || sellerPoints != 6000 {
		t.Fatalf("seller points %d %v", sellerPoints, err)
	}
	if _, err = op(buyer, "buy", listing.ID, nil); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("bought twice: %v", err)
	}
	storage, err := s.ITCStorage(ctx, buyer)
	if err != nil || len(storage) != 1 || storage[0].ID != listing.ID {
		t.Fatalf("buyer storage %+v %v", storage, err)
	}
	if _, err = op(seller, "take", listing.ID, nil); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("seller took the buyer's item: %v", err)
	}
	res, err = op(buyer, "take", listing.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := false
	for _, it := range res.Inventory.Character.Items {
		got = got || it.TemplateID == item.TemplateID
	}
	if !got {
		t.Fatal("taken item is not in the buyer's inventory")
	}
	if storage, _ = s.ITCStorage(ctx, buyer); len(storage) != 0 {
		t.Fatalf("storage not emptied: %+v", storage)
	}

	// A cancelled sale waits in the seller's own storage.
	if _, err = op(seller, "register", 0, register); err != nil {
		t.Fatal(err)
	}
	selling, err := s.ITCSelling(ctx, seller)
	if err != nil || len(selling) != 1 {
		t.Fatalf("selling %+v %v", selling, err)
	}
	if _, err = op(buyer, "cancel", selling[0].ID, nil); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("buyer cancelled the seller's sale: %v", err)
	}
	if _, err = op(seller, "cancel", selling[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if storage, _ = s.ITCStorage(ctx, seller); len(storage) != 1 {
		t.Fatalf("cancelled item not in storage: %+v", storage)
	}

	// An expired sale goes back to its seller too.
	if _, err = op(seller, "take", storage[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = op(seller, "register", 0, register); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_itc_items SET ends_at=clock_timestamp()-interval '1 second' WHERE seller_id=$1 AND state='selling'`, seller.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	if storage, _ = s.ITCStorage(ctx, seller); len(storage) != 1 || storage[0].OwnerID != seller.Session.CharacterID {
		t.Fatalf("expired item not returned: %+v", storage)
	}
}

func TestITCAuctionBidBuyNowAndEnd(t *testing.T) {
	ctx, s, sessions := itcSessions(t)
	seller, buyer := sessions[0], sessions[1]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=20000 WHERE id=$1`, seller.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,4,20,4000000,10)`, seller.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	for _, cs := range sessions {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_cash_wallets(account_id,points) VALUES($1,5000) ON CONFLICT(account_id) DO UPDATE SET points=5000`, cs.Session.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	points := func(cs domain.CashSession) int64 {
		var p int64
		if err := s.db.QueryRowContext(ctx, `SELECT points FROM asset_cash_wallets WHERE account_id=$1`, cs.Session.AccountID).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	seq := 0
	op := func(cs domain.CashSession, r domain.ITCRequest) (domain.ITCResult, error) {
		seq++
		r.Session = cs
		r.OperationID = fmt.Sprintf("itc-auction-%d-%d-%d", cs.Session.CharacterID, time.Now().UnixNano(), seq)
		return s.ChangeITC(ctx, r)
	}
	auction := domain.ITCRequest{Kind: "auction", InvType: 4, Slot: 20, TemplateID: 4000000, Quantity: 2, Price: 100, BuyNowPrice: 1000, BidStep: 10, Hours: 24}
	bad := auction
	bad.BuyNowPrice = 105
	if _, err := op(seller, bad); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("buy-now below start plus step accepted: %v", err)
	}
	res, err := op(seller, auction)
	if err != nil {
		t.Fatal(err)
	}
	id := res.Listing.ID
	page, err := s.ITCList(ctx, buyer, domain.ITCView{Tab: 3})
	if err != nil || page.Total < 1 {
		t.Fatalf("auction not on 競標: %+v %v", page, err)
	}
	if sales, _ := s.ITCList(ctx, buyer, domain.ITCView{Tab: 1}); sales.Total != 0 {
		t.Fatalf("auction on the 販售 list: %+v", sales)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "bid", ListingID: id, Price: 99}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("bid below start accepted: %v", err)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "bid", ListingID: id, Price: 200}); err != nil {
		t.Fatal(err)
	}
	if got, want := points(buyer), int64(5000-200-domain.ITCCommission(200)); got != want {
		t.Fatalf("held %d, want %d", got, want)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "bid", ListingID: id, Price: 300}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("second bid in a row accepted: %v", err)
	}
	if _, err = op(seller, domain.ITCRequest{Kind: "bid", ListingID: id, Price: 300}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("seller bid: %v", err)
	}
	if _, err = op(seller, domain.ITCRequest{Kind: "cancel", ListingID: id}); !errors.Is(err, domain.ErrITCHasBids) {
		t.Fatalf("cancelled with a bid: %v", err)
	}
	// Buying outright returns the held bid first.
	if _, err = op(buyer, domain.ITCRequest{Kind: "buy_now", ListingID: id}); err != nil {
		t.Fatal(err)
	}
	if got, want := points(buyer), int64(5000-1000-domain.ITCCommission(1000)); got != want {
		t.Fatalf("buyer after buy-now %d, want %d", got, want)
	}
	if got := points(seller); got != 6000 {
		t.Fatalf("seller after buy-now %d", got)
	}
	if storage, _ := s.ITCStorage(ctx, buyer); len(storage) != 1 || storage[0].ID != id {
		t.Fatalf("buyer storage %+v", storage)
	}

	// An auction that ends with a bid goes to the bidder and pays the seller.
	res, err = op(seller, auction)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "bid", ListingID: res.Listing.ID, Price: 150}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_itc_items SET ends_at=clock_timestamp()-interval '1 second' WHERE id=$1`, res.Listing.ID); err != nil {
		t.Fatal(err)
	}
	storage, err := s.ITCStorage(ctx, buyer)
	if err != nil || len(storage) != 2 {
		t.Fatalf("won auction not in storage: %+v %v", storage, err)
	}
	if got := points(seller); got != 6150 {
		t.Fatalf("seller after the auction ended %d", got)
	}
}

func TestITCBuyRequestFillWithdrawAndSearch(t *testing.T) {
	ctx, s, sessions := itcSessions(t)
	buyer, seller := sessions[0], sessions[1]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=20000 WHERE id=$1`, buyer.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,4,20,4000000,10)`, seller.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	for _, cs := range sessions {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asset_cash_wallets(account_id,points) VALUES($1,5000) ON CONFLICT(account_id) DO UPDATE SET points=5000`, cs.Session.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	points := func(cs domain.CashSession) int64 {
		var p int64
		if err := s.db.QueryRowContext(ctx, `SELECT points FROM asset_cash_wallets WHERE account_id=$1`, cs.Session.AccountID).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	seq := 0
	op := func(cs domain.CashSession, r domain.ITCRequest) (domain.ITCResult, error) {
		seq++
		r.Session = cs
		r.OperationID = fmt.Sprintf("itc-wanted-%d-%d-%d", cs.Session.CharacterID, time.Now().UnixNano(), seq)
		return s.ChangeITC(ctx, r)
	}
	want := domain.ITCRequest{Kind: "wanted", TemplateID: 4000000, Quantity: 3, Price: 600, Comment: "snail shells"}
	res, err := op(buyer, want)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inventory.Character.Mesos != 20000-domain.ITCRegistrationFee || points(buyer) != 4400 {
		t.Fatalf("fee %d or hold %d wrong", res.Inventory.Character.Mesos, points(buyer))
	}
	id := res.Listing.ID
	page, err := s.ITCList(ctx, seller, domain.ITCView{Tab: 2})
	if err != nil || page.Total != 1 || page.Items[0].Comment != "snail shells" {
		t.Fatalf("購買 tab %+v %v", page, err)
	}
	if mine, _ := s.ITCList(ctx, buyer, domain.ITCView{Tab: 4, Type: 1}); mine.Total != 1 {
		t.Fatalf("等待購買 %+v", mine)
	}
	if selling, _ := s.ITCSelling(ctx, buyer); len(selling) != 0 {
		t.Fatalf("buy request shown as 等待販售: %+v", selling)
	}
	name, _ := s.ITCList(ctx, seller, domain.ITCView{Tab: 2, Search: true, Name: buyer.Session.Character.Name})
	byItem, _ := s.ITCList(ctx, seller, domain.ITCView{Tab: 2, Search: true, Templates: []int{4000000}})
	none, _ := s.ITCList(ctx, seller, domain.ITCView{Tab: 2, Search: true, Templates: []int{4000001}})
	if name.Total != 1 || byItem.Total != 1 || none.Total != 0 {
		t.Fatalf("search by name %d, item %d, other %d", name.Total, byItem.Total, none.Total)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "fill", ListingID: id, InvType: 4, Slot: 20}); !errors.Is(err, domain.ErrForbidden) && !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("buyer filled own request: %v", err)
	}
	res, err = op(seller, domain.ITCRequest{Kind: "fill", ListingID: id, InvType: 4, Slot: 20})
	if err != nil {
		t.Fatal(err)
	}
	offer := res.Listing.ID
	left := 0
	for _, it := range res.Inventory.Character.Items {
		if it.TemplateID == 4000000 {
			left = it.Quantity
		}
	}
	if left != 7 || points(seller) != 5000 {
		t.Fatalf("seller has %d left, %d points before the buyer answers", left, points(seller))
	}
	if _, err = op(seller, domain.ITCRequest{Kind: "fill", ListingID: id, InvType: 4, Slot: 20}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("second item while one waits: %v", err)
	}
	waiting, err := s.ITCList(ctx, buyer, domain.ITCView{Tab: 4, Type: 1})
	if err != nil || waiting.Total != 2 || waiting.Items[0].ID != offer || waiting.Items[1].ID != id {
		t.Fatalf("等待購買 %+v %v", waiting, err)
	}
	// 取消 sends the item back to the seller; the request stays.
	if _, err = op(seller, domain.ITCRequest{Kind: "accept", ListingID: offer}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("seller accepted own offer: %v", err)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "withdraw", ListingID: offer}); err != nil {
		t.Fatal(err)
	}
	back, _ := s.ITCStorage(ctx, seller)
	if len(back) != 1 || back[0].Item.Quantity != 3 {
		t.Fatalf("returned item %+v", back)
	}
	if _, err = op(seller, domain.ITCRequest{Kind: "take", ListingID: back[0].ID}); err != nil {
		t.Fatal(err)
	}
	// 購買 completes the trade.
	if res, err = op(seller, domain.ITCRequest{Kind: "fill", ListingID: id, InvType: 4, Slot: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "accept", ListingID: res.Listing.ID}); err != nil {
		t.Fatal(err)
	}
	if points(seller) != 5000+domain.ITCWantedPayout(600) || points(buyer) != 4400 {
		t.Fatalf("seller %d, buyer %d after 購買", points(seller), points(buyer))
	}
	storage, err := s.ITCStorage(ctx, buyer)
	if err != nil || len(storage) != 1 || storage[0].Item.Quantity != 3 {
		t.Fatalf("buyer storage %+v %v", storage, err)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "take", ListingID: storage[0].ID}); err != nil {
		t.Fatal(err)
	}
	for _, cs := range sessions {
		history, err := s.ITCList(ctx, cs, domain.ITCView{Tab: 4, Type: 2})
		if err != nil || history.Total != 1 || history.Items[0].Price != 600 || history.Items[0].ID >= 0 {
			t.Fatalf("交易紀錄 %+v %v", history, err)
		}
	}

	// A withdrawn or expired request gives the points back.
	res, err = op(buyer, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = op(seller, domain.ITCRequest{Kind: "withdraw", ListingID: res.Listing.ID}); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("seller withdrew the buyer's request: %v", err)
	}
	if _, err = op(buyer, domain.ITCRequest{Kind: "withdraw", ListingID: res.Listing.ID}); err != nil {
		t.Fatal(err)
	}
	if points(buyer) != 4400 {
		t.Fatalf("withdraw refund: %d", points(buyer))
	}
	res, err = op(buyer, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_itc_items SET ends_at=clock_timestamp()-interval '1 second' WHERE id=$1`, res.Listing.ID); err != nil {
		t.Fatal(err)
	}
	if page, _ = s.ITCList(ctx, seller, domain.ITCView{Tab: 2}); page.Total != 0 || points(buyer) != 4400 {
		t.Fatalf("expired request kept %d, points %d", page.Total, points(buyer))
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET mesos=0 WHERE id=$1`, buyer.Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = op(buyer, want); !errors.Is(err, domain.ErrITCMesos) {
		t.Fatalf("fee without mesos: %v", err)
	}
}
