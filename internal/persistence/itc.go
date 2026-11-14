package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"maple-go-server/internal/domain"
)

var _ domain.ITCRepository = (*Store)(nil)

const itcColumns = `id,world_id,seller_id,seller_name,owner_id,state,inv_type,template_id,quantity,equipment,item_expires_at,price,listed_at,ends_at,kind,buy_now_price,bid_step,bid_count,current_price,COALESCE(high_bidder_id,0),comment,COALESCE(request_id,0)`

// itcColumnsI is itcColumns for a query that names the listing table i.
const itcColumnsI = `i.id,i.world_id,i.seller_id,i.seller_name,i.owner_id,i.state,i.inv_type,i.template_id,i.quantity,i.equipment,i.item_expires_at,i.price,i.listed_at,i.ends_at,i.kind,i.buy_now_price,i.bid_step,i.bid_count,i.current_price,COALESCE(i.high_bidder_id,0),i.comment,COALESCE(i.request_id,0)`

func scanITC(row interface{ Scan(...any) error }) (domain.ITCListing, error) {
	var l domain.ITCListing
	var raw []byte
	err := row.Scan(&l.ID, &l.WorldID, &l.SellerID, &l.SellerName, &l.OwnerID, &l.State, &l.Item.InvType, &l.Item.TemplateID, &l.Item.Quantity, &raw, &l.Item.ExpiresAt, &l.Price, &l.ListedAt, &l.EndsAt, &l.Kind, &l.BuyNowPrice, &l.BidStep, &l.BidCount, &l.CurrentPrice, &l.HighBidderID, &l.Comment, &l.RequestID)
	if err != nil {
		return l, err
	}
	if len(raw) > 0 {
		var e domain.Equipment
		if err = json.Unmarshal(raw, &e); err != nil {
			return l, err
		}
		l.Equipment = &e
	}
	return l, nil
}

func scanITCRows(rows *sql.Rows, err error) ([]domain.ITCListing, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ITCListing, 0)
	for rows.Next() {
		l, err := scanITC(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

type itcExec interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// expireITC ends every listing past its end. An auction with a bid goes to
// the high bidder's 拍賣倉庫 and its seller is paid the winning bid (the
// bidder paid in advance); everything else goes back to the seller.
func expireITC(ctx context.Context, q itcExec, worldID int) error {
	// Items waiting on a request that runs out go back to their sellers.
	if _, err := q.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',kind='sale',owner_id=seller_id,request_id=NULL,revision=revision+1 WHERE world_id=$1 AND state='selling' AND kind='offer' AND request_id IN (SELECT id FROM asset_itc_items WHERE world_id=$1 AND state='selling' AND kind='wanted' AND ends_at<=clock_timestamp())`, worldID); err != nil {
		return err
	}
	// A buy request that runs out gives the buyer back what they paid.
	if _, err := q.ExecContext(ctx, `WITH gone AS (DELETE FROM asset_itc_items WHERE world_id=$1 AND state='selling' AND kind='wanted' AND ends_at<=clock_timestamp() RETURNING seller_account_id,held_points)
INSERT INTO asset_cash_wallets(account_id,points) SELECT seller_account_id,LEAST(sum(held_points),2147483647) FROM gone GROUP BY seller_account_id
ON CONFLICT(account_id) DO UPDATE SET points=LEAST(asset_cash_wallets.points+EXCLUDED.points,2147483647)`, worldID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `WITH won AS (UPDATE asset_itc_items SET state='storage',owner_id=high_bidder_id,buyer_id=high_bidder_id,held_points=0,revision=revision+1 WHERE world_id=$1 AND state='selling' AND kind='auction' AND high_bidder_id IS NOT NULL AND ends_at<=clock_timestamp() RETURNING seller_account_id,current_price,seller_id,seller_name,high_bidder_id,world_id,inv_type,template_id,quantity),
bought AS (INSERT INTO asset_itc_history(character_id,world_id,side,how,inv_type,template_id,quantity,price,counterpart) SELECT high_bidder_id,world_id,'bought','auction',inv_type,template_id,quantity,current_price,seller_name FROM won),
sold AS (INSERT INTO asset_itc_history(character_id,world_id,side,how,inv_type,template_id,quantity,price,counterpart) SELECT won.seller_id,won.world_id,'sold','auction',won.inv_type,won.template_id,won.quantity,won.current_price,COALESCE(g.name,'') FROM won LEFT JOIN game_characters g ON g.id=won.high_bidder_id)
INSERT INTO asset_cash_wallets(account_id,points) SELECT seller_account_id,LEAST(sum(current_price),2147483647) FROM won GROUP BY seller_account_id
ON CONFLICT(account_id) DO UPDATE SET points=LEAST(asset_cash_wallets.points+EXCLUDED.points,2147483647)`, worldID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',owner_id=seller_id,revision=revision+1 WHERE world_id=$1 AND state='selling' AND ends_at<=clock_timestamp()`, worldID)
	return err
}

// itcInvType maps a 販售 sub tab (全部, 裝備, 消耗, 裝飾, 其他) to an
// inventory type; 0 is every type.
func itcInvType(subTab int) int {
	if subTab >= 1 && subTab <= 4 {
		return subTab
	}
	return 0
}

func (s *Store) ITCList(ctx context.Context, c domain.CashSession, v domain.ITCView) (domain.ITCPage, error) {
	page := domain.ITCPage{Items: make([]domain.ITCListing, 0)}
	if v.Page < 0 || v.Page > 10000 {
		return page, domain.ErrInvalidInput
	}
	world := c.Session.Lease.WorldID
	if err := expireITC(ctx, s.db, world); err != nil {
		return page, err
	}
	offset := v.Page * domain.ITCPageSize
	where, args := "", []any{}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	from := "asset_itc_items i"
	order := "i.id DESC"
	switch {
	case v.Tab >= 1 && v.Tab <= 3:
		// 販售 lists sales, 購買 buy requests, 競標 auctions.
		kind := map[int]string{1: domain.ITCSale, 2: domain.ITCWanted, 3: domain.ITCAuction}[v.Tab]
		where = "i.world_id=" + arg(world) + " AND i.state='selling' AND i.kind=" + arg(kind)
		if inv := itcInvType(v.Type); inv != 0 {
			where += " AND i.inv_type=" + arg(inv)
		}
		if v.Search {
			if v.Name != "" {
				where += " AND i.seller_name=" + arg(v.Name)
			} else {
				if len(v.Templates) == 0 {
					return page, nil
				}
				ids := make([]int64, len(v.Templates))
				for i, id := range v.Templates {
					ids[i] = int64(id)
				}
				where += " AND i.template_id=ANY(" + arg(ids) + ")"
			}
		}
	case v.Tab == 4 && v.Type == 0:
		// 我的拍賣 / 購物車: carted listings still on sale.
		from = "asset_itc_cart c JOIN asset_itc_items i ON i.id=c.item_id"
		where = "c.character_id=" + arg(c.Session.CharacterID) + " AND i.state='selling'"
		order = "c.added_at DESC"
	case v.Tab == 4 && v.Type == 1:
		// 等待購買: items sold to the character's buy requests, waiting for
		// 購買 or 取消, then the open requests themselves.
		me := arg(c.Session.CharacterID)
		where = "i.state='selling' AND ((i.kind='offer' AND i.request_id IN (SELECT id FROM asset_itc_items WHERE seller_id=" + me + " AND kind='wanted')) OR (i.kind='wanted' AND i.seller_id=" + me + "))"
		order = "(i.kind='wanted'), i.id DESC"
	case v.Tab == 4 && v.Type == 2:
		// 交易紀錄: finished trades, newest first.
		return s.itcHistory(ctx, c, offset)
	case v.Tab == 4 && v.Type == 3:
		// 交易進行: auctions the character leads.
		where = "i.high_bidder_id=" + arg(c.Session.CharacterID) + " AND i.state='selling' AND i.kind='auction'"
	default:
		return page, nil
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+from+` WHERE `+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	limit, skip := arg(domain.ITCPageSize), arg(offset)
	items, err := scanITCRows(s.db.QueryContext(ctx, `SELECT `+itcColumnsI+` FROM `+from+` WHERE `+where+` ORDER BY `+order+` LIMIT `+limit+` OFFSET `+skip, args...))
	if err != nil {
		return page, err
	}
	if v.Tab == 4 && v.Type == 3 {
		for i := range items {
			items[i].Status = 2 // 下標
		}
	}
	page.Items = items
	return page, nil
}

func (s *Store) ITCSelling(ctx context.Context, c domain.CashSession) ([]domain.ITCListing, error) {
	if err := expireITC(ctx, s.db, c.Session.Lease.WorldID); err != nil {
		return nil, err
	}
	return scanITCRows(s.db.QueryContext(ctx, `SELECT `+itcColumns+` FROM asset_itc_items WHERE seller_id=$1 AND state='selling' AND kind<>'wanted' ORDER BY id`, c.Session.CharacterID))
}

func (s *Store) ITCStorage(ctx context.Context, c domain.CashSession) ([]domain.ITCListing, error) {
	if err := expireITC(ctx, s.db, c.Session.Lease.WorldID); err != nil {
		return nil, err
	}
	return scanITCRows(s.db.QueryContext(ctx, `SELECT `+itcColumns+` FROM asset_itc_items WHERE owner_id=$1 AND state='storage' ORDER BY id`, c.Session.CharacterID))
}

// lockITCCharacter fences the cash shop session and locks the character row,
// in the same order as the cash shop's own inventory moves.
func lockITCCharacter(ctx context.Context, tx *sql.Tx, c domain.CashSession) (domain.Character, error) {
	if err := guardCashSession(ctx, tx, c); err != nil {
		return domain.Character{}, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 FOR UPDATE`, c.Session.CharacterID).Scan(&id); err != nil {
		return domain.Character{}, err
	}
	value, err := readCharacterSQL(ctx, tx, id)
	if err == nil && (value.AccountID != c.Session.AccountID || value.WorldID != c.Session.Lease.WorldID) {
		err = domain.ErrForbidden
	}
	return value, err
}

func lockITCListing(ctx context.Context, tx *sql.Tx, id int64, world int) (domain.ITCListing, error) {
	l, err := scanITC(tx.QueryRowContext(ctx, `SELECT `+itcColumns+` FROM asset_itc_items WHERE id=$1 AND world_id=$2 FOR UPDATE`, id, world))
	if errors.Is(err, sql.ErrNoRows) {
		return l, domain.ErrNotFound
	}
	return l, err
}

// ChangeITC runs one trading center command under one SQL commit. The
// character is locked first, then the listing, then the wallets in account
// order, so two buyers or a buyer and the seller cannot deadlock.
func (s *Store) ChangeITC(ctx context.Context, r domain.ITCRequest) (domain.ITCResult, error) {
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 {
		return domain.ITCResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ITCResult{}, err
	}
	defer tx.Rollback()
	c, err := lockITCCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.ITCResult{}, err
	}
	if err = expireITC(ctx, tx, c.WorldID); err != nil {
		return domain.ITCResult{}, err
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO ops_itc_operations(operation_id,character_id,kind,item_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, r.OperationID, c.ID, r.Kind, r.ListingID)
	if err != nil {
		return domain.ITCResult{}, err
	}
	if n, _ := inserted.RowsAffected(); n != 1 {
		return domain.ITCResult{}, domain.ErrAlreadyExists
	}
	before := c
	result := domain.ITCResult{Inventory: domain.InventoryResult{Character: c}}
	inventoryChanged := false
	switch r.Kind {
	case "register", "auction":
		err = itcRegister(ctx, tx, r, &result)
		inventoryChanged = err == nil
	case "buy":
		err = itcBuy(ctx, tx, r, &result)
	case "bid":
		err = itcBid(ctx, tx, r, &result)
	case "buy_now":
		err = itcBuyNow(ctx, tx, r, &result)
	case "wanted":
		err = itcWanted(ctx, tx, r, &result)
		inventoryChanged = err == nil
	case "fill":
		err = itcFill(ctx, tx, r, &result)
		inventoryChanged = err == nil
	case "withdraw":
		err = itcWithdraw(ctx, tx, r, &result)
	case "accept":
		err = itcAccept(ctx, tx, r, &result)
	case "cancel":
		err = itcCancel(ctx, tx, r, &result)
	case "take":
		err = itcTake(ctx, tx, r, &result)
		inventoryChanged = err == nil
	case "cart_add":
		err = itcCartAdd(ctx, tx, r, &result)
	case "cart_remove":
		err = itcCartRemove(ctx, tx, r, &result)
	default:
		err = domain.ErrInvalidInput
	}
	if err != nil {
		return domain.ITCResult{}, err
	}
	if inventoryChanged {
		if err = commitInventoryState(ctx, tx, &result.Inventory.Character); err != nil {
			return domain.ITCResult{}, err
		}
		if err = appendEconomicLedger(ctx, tx, "itc/"+r.OperationID, before, result.Inventory.Character, "itc_"+r.Kind); err != nil {
			return domain.ITCResult{}, err
		}
	}
	if result.Wallet, err = readCashWalletSQL(ctx, tx, c.AccountID); err != nil {
		return domain.ITCResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.ITCResult{}, err
	}
	if inventoryChanged {
		s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	}
	return result, nil
}

func itcRegister(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	if r.Price < domain.ITCMinPrice || r.Price > domain.ITCMaxPrice || r.InvType < 1 || r.InvType > 4 || r.Quantity < 1 || r.Quantity > 30000 || (r.InvType == 1 && r.Quantity != 1) {
		return domain.ErrInvalidInput
	}
	auction := r.Kind == "auction"
	duration := domain.ITCSaleDuration
	if auction {
		// 0x5608C6: the buy-now price is at least the start plus one step.
		if r.Hours < domain.ITCMinHours || r.Hours > domain.ITCMaxHours || r.BidStep < 1 || r.BuyNowPrice > domain.ITCMaxPrice || r.BuyNowPrice < r.Price+int64(r.BidStep) {
			return domain.ErrInvalidInput
		}
		duration = time.Duration(r.Hours) * time.Hour
	}
	c := &result.Inventory.Character
	var selling int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_itc_items WHERE seller_id=$1 AND state='selling'`, c.ID).Scan(&selling); err != nil {
		return err
	}
	if selling >= domain.ITCSellingLimit {
		return domain.ErrCashLimit
	}
	if c.Mesos < domain.ITCRegistrationFee {
		return domain.ErrInsufficient
	}
	var instance, cashID int64
	template := 0
	if r.InvType == 1 {
		for _, e := range c.Equipment {
			if e.Slot == r.Slot {
				instance, cashID, template = e.ID, e.CashID, e.ItemID
			}
		}
	} else {
		for _, item := range c.Items {
			if item.InvType == r.InvType && item.Slot == r.Slot {
				instance, cashID, template = item.ID, item.CashID, item.TemplateID
			}
		}
	}
	if instance == 0 || template != r.TemplateID {
		return domain.ErrNotFound
	}
	if cashID > 0 {
		// Cash items are not sold here (Legacy MTSOperation refuses them too).
		return domain.ErrForbidden
	}
	taken, err := takeInventoryItems(ctx, tx, &result.Inventory, []domain.TradeItem{{InvType: r.InvType, Slot: r.Slot, Quantity: r.Quantity, InstanceID: instance, TradeSlot: 1}}, true)
	if err != nil {
		return err
	}
	l := domain.ITCListing{WorldID: c.WorldID, SellerID: c.ID, SellerName: c.Name, OwnerID: c.ID, State: domain.ITCSelling, Price: r.Price, Equipment: taken[0].Equipment, Kind: domain.ITCSale}
	if auction {
		l.Kind, l.BuyNowPrice, l.BidStep, l.CurrentPrice = domain.ITCAuction, r.BuyNowPrice, r.BidStep, r.Price
	}
	if taken[0].Equipment != nil {
		l.Item = domain.InventoryItem{InvType: 1, TemplateID: taken[0].Equipment.ItemID, Quantity: 1, ExpiresAt: taken[0].Equipment.ExpiresAt}
	} else {
		l.Item = domain.InventoryItem{InvType: r.InvType, TemplateID: taken[0].Item.TemplateID, Quantity: r.Quantity, ExpiresAt: taken[0].Item.ExpiresAt}
	}
	if l.Item.ExpiresAt != 0 {
		// A timed item would run out while it waits on the list.
		return domain.ErrForbidden
	}
	c.Mesos -= domain.ITCRegistrationFee
	var equipment []byte
	if l.Equipment != nil {
		equipment, _ = json.Marshal(l.Equipment)
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_itc_items(world_id,seller_id,seller_account_id,seller_name,owner_id,state,inv_type,template_id,quantity,equipment,item_expires_at,price,ends_at,kind,buy_now_price,bid_step,current_price) VALUES($1,$2,$3,$4,$2,'selling',$5,$6,$7,$8,$9,$10,clock_timestamp()+$11*interval '1 second',$12,$13,$14,$15) RETURNING id,listed_at,ends_at`,
		l.WorldID, c.ID, c.AccountID, c.Name, l.Item.InvType, l.Item.TemplateID, l.Item.Quantity, nullableJSON(equipment), l.Item.ExpiresAt, l.Price, int64(duration/time.Second), l.Kind, l.BuyNowPrice, l.BidStep, l.CurrentPrice).Scan(&l.ID, &l.ListedAt, &l.EndsAt); err != nil {
		return err
	}
	result.Listing = l
	return nil
}

func itcBuy(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, err := lockITCListing(ctx, tx, r.ListingID, c.WorldID)
	if err != nil {
		return err
	}
	if l.State != domain.ITCSelling || l.Kind != domain.ITCSale {
		return domain.ErrStaleOwner
	}
	if l.SellerID == c.ID {
		return domain.ErrForbidden
	}
	var sellerAccount int64
	if err = tx.QueryRowContext(ctx, `SELECT seller_account_id FROM asset_itc_items WHERE id=$1`, l.ID).Scan(&sellerAccount); err != nil {
		return err
	}
	if sellerAccount == c.AccountID {
		return domain.ErrForbidden
	}
	total := l.Price + l.Commission()
	// Lock both wallets in account order.
	first, second := c.AccountID, sellerAccount
	if second < first {
		first, second = second, first
	}
	wallets := map[int64]domain.CashWallet{}
	for _, account := range []int64{first, second} {
		w, err := readCashWalletSQL(ctx, tx, account)
		if err != nil {
			return err
		}
		wallets[account] = w
	}
	buyer, seller := wallets[c.AccountID], wallets[sellerAccount]
	if buyer.Points < total {
		return domain.ErrInsufficient
	}
	if seller.Points > math.MaxInt32-l.Price {
		return domain.ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points-$1 WHERE account_id=$2`, total, c.AccountID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points+$1 WHERE account_id=$2`, l.Price, sellerAccount); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',owner_id=$1,buyer_id=$1,revision=revision+1 WHERE id=$2`, c.ID, l.ID); err != nil {
		return err
	}
	if err = itcRecord(ctx, tx, l, "sale", c.ID, c.Name, l.SellerID, l.SellerName, l.Price); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_itc_cart WHERE item_id=$1`, l.ID); err != nil {
		return err
	}
	l.State, l.OwnerID = domain.ITCStorage, c.ID
	result.Listing = l
	return nil
}

func itcCancel(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, err := lockITCListing(ctx, tx, r.ListingID, c.WorldID)
	if err != nil {
		return err
	}
	if l.State != domain.ITCSelling || l.SellerID != c.ID || l.Kind == domain.ITCWanted {
		return domain.ErrStaleOwner
	}
	if l.BidCount > 0 {
		// 已有1件以上的出價紀錄！因此無法取消販售登記！
		return domain.ErrITCHasBids
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',owner_id=seller_id,revision=revision+1 WHERE id=$1`, l.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_itc_cart WHERE item_id=$1`, l.ID); err != nil {
		return err
	}
	l.State = domain.ITCStorage
	result.Listing = l
	return nil
}

func itcTake(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, err := lockITCListing(ctx, tx, r.ListingID, c.WorldID)
	if err != nil {
		return err
	}
	if l.State != domain.ITCStorage || l.OwnerID != c.ID {
		return domain.ErrStaleOwner
	}
	item := l.Item
	if err = receiveTradeItem(ctx, tx, &result.Inventory, domain.TradeItem{InvType: item.InvType, Quantity: item.Quantity, Equipment: l.Equipment, Item: &item}); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_itc_items WHERE id=$1`, l.ID); err != nil {
		return err
	}
	result.Listing = l
	return nil
}

func itcCartAdd(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, err := lockITCListing(ctx, tx, r.ListingID, c.WorldID)
	if err != nil {
		return err
	}
	if l.State != domain.ITCSelling || l.SellerID == c.ID || l.Kind == domain.ITCWanted {
		return domain.ErrForbidden
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_itc_cart WHERE character_id=$1`, c.ID).Scan(&count); err != nil {
		return err
	}
	if count >= domain.ITCCartLimit {
		return domain.ErrCashLimit
	}
	added, err := tx.ExecContext(ctx, `INSERT INTO asset_itc_cart(character_id,item_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.ID, l.ID)
	if err != nil {
		return err
	}
	if n, _ := added.RowsAffected(); n != 1 {
		return domain.ErrAlreadyExists
	}
	result.Listing = l
	return nil
}

func itcCartRemove(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	removed, err := tx.ExecContext(ctx, `DELETE FROM asset_itc_cart WHERE character_id=$1 AND item_id=$2`, result.Inventory.Character.ID, r.ListingID)
	if err != nil {
		return err
	}
	if n, _ := removed.RowsAffected(); n != 1 {
		return domain.ErrNotFound
	}
	return nil
}

// lockITCWallets locks the wallets of the given accounts (0 skipped) in
// account order and returns them by account.
func lockITCWallets(ctx context.Context, tx *sql.Tx, accounts ...int64) (map[int64]domain.CashWallet, error) {
	ordered := append([]int64(nil), accounts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	out := make(map[int64]domain.CashWallet, len(ordered))
	for _, account := range ordered {
		if _, done := out[account]; done || account == 0 {
			continue
		}
		w, err := readCashWalletSQL(ctx, tx, account)
		if err != nil {
			return nil, err
		}
		out[account] = w
	}
	return out, nil
}

// lockITCAuction locks an open auction somebody other than its seller may
// bid on, with the seller's account, the high bidder's account and what the
// high bidder holds.
func lockITCAuction(ctx context.Context, tx *sql.Tx, c *domain.Character, id int64) (domain.ITCListing, int64, int64, int64, error) {
	l, err := lockITCListing(ctx, tx, id, c.WorldID)
	if err != nil {
		return l, 0, 0, 0, err
	}
	if l.State != domain.ITCSelling || l.Kind != domain.ITCAuction {
		return l, 0, 0, 0, domain.ErrStaleOwner
	}
	var sellerAccount, previousAccount, held int64
	if err = tx.QueryRowContext(ctx, `SELECT seller_account_id,COALESCE(high_bidder_account_id,0),held_points FROM asset_itc_items WHERE id=$1`, l.ID).Scan(&sellerAccount, &previousAccount, &held); err != nil {
		return l, 0, 0, 0, err
	}
	if l.SellerID == c.ID || sellerAccount == c.AccountID {
		return l, 0, 0, 0, domain.ErrForbidden
	}
	return l, sellerAccount, previousAccount, held, nil
}

// itcBid holds the bid plus commission from the bidder and gives the last
// high bidder back what they held. Bids stay below the buy-now price; the
// client buys outright instead (0x56906A).
func itcBid(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, _, previousAccount, held, err := lockITCAuction(ctx, tx, c, r.ListingID)
	if err != nil {
		return err
	}
	if l.HighBidderID == c.ID {
		// 無法連續下標
		return domain.ErrAlreadyExists
	}
	if r.Price < l.MinimumBid() || r.Price >= l.BuyNowPrice {
		return domain.ErrStaleOwner
	}
	total := r.Price + domain.ITCCommission(r.Price)
	wallets, err := lockITCWallets(ctx, tx, c.AccountID, previousAccount)
	if err != nil {
		return err
	}
	if wallets[c.AccountID].Points < total {
		return domain.ErrInsufficient
	}
	if previousAccount != 0 && held > 0 {
		if wallets[previousAccount].Points > math.MaxInt32-held {
			return domain.ErrForbidden
		}
		if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points+$1 WHERE account_id=$2`, held, previousAccount); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points-$1 WHERE account_id=$2`, total, c.AccountID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_itc_items SET current_price=$1,bid_count=bid_count+1,high_bidder_id=$2,high_bidder_account_id=$3,held_points=$4,revision=revision+1 WHERE id=$5`, r.Price, c.ID, c.AccountID, total, l.ID); err != nil {
		return err
	}
	l.CurrentPrice, l.BidCount, l.HighBidderID = r.Price, l.BidCount+1, c.ID
	result.Listing = l
	return nil
}

// itcBuyNow ends an auction at its buy-now price for the caller.
func itcBuyNow(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, sellerAccount, previousAccount, held, err := lockITCAuction(ctx, tx, c, r.ListingID)
	if err != nil {
		return err
	}
	if l.BuyNowPrice <= 0 {
		return domain.ErrStaleOwner
	}
	total := l.BuyNowPrice + domain.ITCCommission(l.BuyNowPrice)
	wallets, err := lockITCWallets(ctx, tx, c.AccountID, sellerAccount, previousAccount)
	if err != nil {
		return err
	}
	refund := int64(0)
	if previousAccount == c.AccountID {
		// The high bidder buying outright gets the held bid back first.
		refund = held
	}
	if wallets[c.AccountID].Points+refund < total {
		return domain.ErrInsufficient
	}
	if wallets[sellerAccount].Points > math.MaxInt32-l.BuyNowPrice {
		return domain.ErrForbidden
	}
	if previousAccount != 0 && held > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points+$1 WHERE account_id=$2`, held, previousAccount); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points-$1 WHERE account_id=$2`, total, c.AccountID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points+$1 WHERE account_id=$2`, l.BuyNowPrice, sellerAccount); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',owner_id=$1,buyer_id=$1,current_price=buy_now_price,high_bidder_id=$1,high_bidder_account_id=$2,held_points=0,revision=revision+1 WHERE id=$3`, c.ID, c.AccountID, l.ID); err != nil {
		return err
	}
	if err = itcRecord(ctx, tx, l, "auction", c.ID, c.Name, l.SellerID, l.SellerName, l.BuyNowPrice); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_itc_cart WHERE item_id=$1`, l.ID); err != nil {
		return err
	}
	l.State, l.OwnerID = domain.ITCStorage, c.ID
	result.Listing = l
	return nil
}

// itcWanted registers a buy request: the buyer pays the registration fee in
// mesos and the price in 楓葉點數 in advance.
func itcWanted(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	inv := r.TemplateID / 1000000
	if r.Price < domain.ITCMinPrice || r.Price > domain.ITCMaxPrice || inv < 1 || inv > 4 || r.Quantity < 1 || r.Quantity > 30000 || (inv == 1 && r.Quantity != 1) || len(r.Comment) > 64 {
		return domain.ErrInvalidInput
	}
	if _, err := inventoryDefinition(r.TemplateID); err != nil {
		return domain.ErrInvalidInput
	}
	c := &result.Inventory.Character
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_itc_items WHERE seller_id=$1 AND state='selling' AND kind='wanted'`, c.ID).Scan(&open); err != nil {
		return err
	}
	if open >= domain.ITCSellingLimit {
		return domain.ErrCashLimit
	}
	if c.Mesos < domain.ITCRegistrationFee {
		return domain.ErrITCMesos
	}
	wallets, err := lockITCWallets(ctx, tx, c.AccountID)
	if err != nil {
		return err
	}
	if wallets[c.AccountID].Points < r.Price {
		return domain.ErrInsufficient
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points-$1 WHERE account_id=$2`, r.Price, c.AccountID); err != nil {
		return err
	}
	c.Mesos -= domain.ITCRegistrationFee
	l := domain.ITCListing{WorldID: c.WorldID, SellerID: c.ID, SellerName: c.Name, OwnerID: c.ID, State: domain.ITCSelling, Kind: domain.ITCWanted, Price: r.Price, Comment: r.Comment,
		Item: domain.InventoryItem{InvType: inv, TemplateID: r.TemplateID, Quantity: r.Quantity}}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_itc_items(world_id,seller_id,seller_account_id,seller_name,owner_id,state,inv_type,template_id,quantity,price,ends_at,kind,held_points,comment) VALUES($1,$2,$3,$4,$2,'selling',$5,$6,$7,$8,clock_timestamp()+$9*interval '1 second','wanted',$8,$10) RETURNING id,listed_at,ends_at`,
		l.WorldID, c.ID, c.AccountID, c.Name, inv, r.TemplateID, r.Quantity, r.Price, int64(domain.ITCSaleDuration/time.Second), r.Comment).Scan(&l.ID, &l.ListedAt, &l.EndsAt); err != nil {
		return err
	}
	result.Listing = l
	return nil
}

// itcFill sells an inventory item to a buy request (購買 tab, 0x5675F9): the
// item goes to the buyer's 拍賣倉庫 and the seller gets the price less the
// 10% fee.
func itcFill(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, err := lockITCListing(ctx, tx, r.ListingID, c.WorldID)
	if err != nil {
		return err
	}
	if l.State != domain.ITCSelling || l.Kind != domain.ITCWanted {
		return domain.ErrStaleOwner
	}
	var buyerAccount int64
	if err = tx.QueryRowContext(ctx, `SELECT seller_account_id FROM asset_itc_items WHERE id=$1`, l.ID).Scan(&buyerAccount); err != nil {
		return err
	}
	if l.SellerID == c.ID || buyerAccount == c.AccountID {
		return domain.ErrForbidden
	}
	if r.InvType != l.Item.InvType {
		return domain.ErrNotFound
	}
	var instance, cashID int64
	template, have := 0, 0
	if r.InvType == 1 {
		for _, e := range c.Equipment {
			if e.Slot == r.Slot {
				instance, cashID, template, have = e.ID, e.CashID, e.ItemID, 1
			}
		}
	} else {
		for _, item := range c.Items {
			if item.InvType == r.InvType && item.Slot == r.Slot {
				instance, cashID, template, have = item.ID, item.CashID, item.TemplateID, item.Quantity
			}
		}
	}
	if instance == 0 || template != l.Item.TemplateID {
		return domain.ErrNotFound
	}
	if have < l.Item.Quantity {
		return domain.ErrInvalidInput
	}
	if cashID > 0 {
		return domain.ErrForbidden
	}
	taken, err := takeInventoryItems(ctx, tx, &result.Inventory, []domain.TradeItem{{InvType: r.InvType, Slot: r.Slot, Quantity: l.Item.Quantity, InstanceID: instance, TradeSlot: 1}}, true)
	if err != nil {
		return err
	}
	item := l.Item
	if taken[0].Item != nil {
		item.ExpiresAt = taken[0].Item.ExpiresAt
	} else if taken[0].Equipment != nil {
		item.ExpiresAt = taken[0].Equipment.ExpiresAt
	}
	if item.ExpiresAt != 0 {
		return domain.ErrForbidden
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_itc_items WHERE request_id=$1 AND kind='offer' AND state='selling'`, l.ID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		// One item at a time waits for the buyer.
		return domain.ErrStaleOwner
	}
	var equipment []byte
	if taken[0].Equipment != nil {
		equipment, _ = json.Marshal(taken[0].Equipment)
	}
	// The item waits under the buyer's 等待購買 until 購買 or 取消.
	offer := domain.ITCListing{WorldID: c.WorldID, SellerID: c.ID, SellerName: c.Name, OwnerID: c.ID, State: domain.ITCSelling, Kind: domain.ITCOffer, Price: l.Price, Item: item, Equipment: taken[0].Equipment, RequestID: l.ID, EndsAt: l.EndsAt}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_itc_items(world_id,seller_id,seller_account_id,seller_name,owner_id,state,inv_type,template_id,quantity,equipment,price,ends_at,kind,request_id) VALUES($1,$2,$3,$4,$2,'selling',$5,$6,$7,$8,$9,$10,'offer',$11) RETURNING id,listed_at`,
		c.WorldID, c.ID, c.AccountID, c.Name, item.InvType, item.TemplateID, item.Quantity, nullableJSON(equipment), l.Price, l.EndsAt, l.ID).Scan(&offer.ID, &offer.ListedAt); err != nil {
		return err
	}
	result.Listing = offer
	return nil
}

// itcWithdraw cancels the character's own buy request and returns the
// points held for it.
func itcWithdraw(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	l, err := lockITCListing(ctx, tx, r.ListingID, c.WorldID)
	if err != nil {
		return err
	}
	if l.State == domain.ITCSelling && l.Kind == domain.ITCOffer {
		// 取消 on an item sold to the character's request sends it back.
		var owner int64
		if err = tx.QueryRowContext(ctx, `SELECT seller_id FROM asset_itc_items WHERE id=$1 AND kind='wanted' FOR UPDATE`, l.RequestID).Scan(&owner); err != nil || owner != c.ID {
			return domain.ErrStaleOwner
		}
		if _, err = tx.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',kind='sale',owner_id=seller_id,request_id=NULL,revision=revision+1 WHERE id=$1`, l.ID); err != nil {
			return err
		}
		result.Listing = l
		return nil
	}
	if l.State != domain.ITCSelling || l.Kind != domain.ITCWanted || l.SellerID != c.ID {
		return domain.ErrStaleOwner
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',kind='sale',owner_id=seller_id,request_id=NULL,revision=revision+1 WHERE request_id=$1 AND kind='offer' AND state='selling'`, l.ID); err != nil {
		return err
	}
	var held int64
	if err = tx.QueryRowContext(ctx, `SELECT held_points FROM asset_itc_items WHERE id=$1`, l.ID).Scan(&held); err != nil {
		return err
	}
	wallets, err := lockITCWallets(ctx, tx, c.AccountID)
	if err != nil {
		return err
	}
	if wallets[c.AccountID].Points > math.MaxInt32-held {
		return domain.ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points+$1 WHERE account_id=$2`, held, c.AccountID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_itc_items WHERE id=$1`, l.ID); err != nil {
		return err
	}
	result.Listing = l
	return nil
}

// itcRecord writes both sides of a finished trade to 交易紀錄.
func itcRecord(ctx context.Context, tx *sql.Tx, l domain.ITCListing, how string, buyerID int64, buyerName string, sellerID int64, sellerName string, price int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO asset_itc_history(character_id,world_id,side,how,inv_type,template_id,quantity,price,counterpart) VALUES($1,$2,'bought',$3,$4,$5,$6,$7,$8),($9,$2,'sold',$3,$4,$5,$6,$7,$10)`,
		buyerID, l.WorldID, how, l.Item.InvType, l.Item.TemplateID, l.Item.Quantity, price, sellerName, sellerID, buyerName)
	return err
}

// itcHistory reads one page of 交易紀錄 as list rows: negative ids (the
// client ignores their buttons), the other side's name and the trade time.
func (s *Store) itcHistory(ctx context.Context, c domain.CashSession, offset int) (domain.ITCPage, error) {
	page := domain.ITCPage{Items: make([]domain.ITCListing, 0)}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_itc_history WHERE character_id=$1`, c.Session.CharacterID).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,world_id,inv_type,template_id,quantity,price,counterpart,at,side='bought' FROM asset_itc_history WHERE character_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`, c.Session.CharacterID, domain.ITCPageSize, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		l := domain.ITCListing{Kind: domain.ITCHistory, State: domain.ITCStorage}
		var bought bool
		if err = rows.Scan(&l.ID, &l.WorldID, &l.Item.InvType, &l.Item.TemplateID, &l.Item.Quantity, &l.Price, &l.SellerName, &l.EndsAt, &bought); err != nil {
			return page, err
		}
		if bought {
			l.Status = 1
		}
		l.ID = -l.ID
		l.ListedAt = l.EndsAt
		page.Items = append(page.Items, l)
	}
	return page, rows.Err()
}

// itcAccept is 購買 under 等待購買: the buyer takes an item sold to their
// request. The held price pays the seller less the 10% fee and the request
// is done.
func itcAccept(ctx context.Context, tx *sql.Tx, r domain.ITCRequest, result *domain.ITCResult) error {
	c := &result.Inventory.Character
	offer, err := lockITCListing(ctx, tx, r.ListingID, c.WorldID)
	if err != nil {
		return err
	}
	if offer.State != domain.ITCSelling || offer.Kind != domain.ITCOffer {
		return domain.ErrStaleOwner
	}
	request, err := lockITCListing(ctx, tx, offer.RequestID, c.WorldID)
	if err != nil {
		return domain.ErrStaleOwner
	}
	if request.State != domain.ITCSelling || request.Kind != domain.ITCWanted || request.SellerID != c.ID {
		return domain.ErrStaleOwner
	}
	var sellerAccount, held int64
	if err = tx.QueryRowContext(ctx, `SELECT o.seller_account_id,w.held_points FROM asset_itc_items o, asset_itc_items w WHERE o.id=$1 AND w.id=$2`, offer.ID, request.ID).Scan(&sellerAccount, &held); err != nil {
		return err
	}
	payout := domain.ITCWantedPayout(request.Price)
	wallets, err := lockITCWallets(ctx, tx, c.AccountID, sellerAccount)
	if err != nil {
		return err
	}
	if wallets[sellerAccount].Points > math.MaxInt32-payout {
		return domain.ErrForbidden
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points+$1 WHERE account_id=$2`, payout, sellerAccount); err != nil {
		return err
	}
	if extra := held - request.Price; extra > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET points=points+$1 WHERE account_id=$2`, extra, c.AccountID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_itc_items SET state='storage',kind='sale',owner_id=$1,buyer_id=$1,request_id=NULL,revision=revision+1 WHERE id=$2`, c.ID, offer.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM asset_itc_items WHERE id=$1`, request.ID); err != nil {
		return err
	}
	if err = itcRecord(ctx, tx, offer, "request", c.ID, c.Name, offer.SellerID, offer.SellerName, request.Price); err != nil {
		return err
	}
	offer.State, offer.OwnerID = domain.ITCStorage, c.ID
	result.Listing = offer
	return nil
}
