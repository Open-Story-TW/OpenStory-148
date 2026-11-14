package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"maple-go-server/internal/domain"
)

var _ domain.MerchantRepository = (*Store)(nil)

// merchantSalesShown bounds the sold-items list the owner dialog receives
// (the native count is one byte).
const merchantSalesShown = 100

type merchantQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const merchantColumns = `id,character_id,account_id,world_id,channel_id,map_id,x,y,foothold,permit_item_id,owner_name,title,state,mesos,revision,opened_at,expires_at`

func scanMerchant(row interface{ Scan(...any) error }) (domain.MerchantShop, error) {
	var m domain.MerchantShop
	err := row.Scan(&m.ID, &m.CharacterID, &m.AccountID, &m.WorldID, &m.ChannelID, &m.MapID, &m.X, &m.Y, &m.Foothold, &m.PermitItemID, &m.OwnerName, &m.Title, &m.State, &m.Mesos, &m.Revision, &m.OpenedAt, &m.ExpiresAt)
	return m, err
}

// readMerchantDetail fills the item rows and the latest sales of a shop.
func readMerchantDetail(ctx context.Context, q merchantQuery, m *domain.MerchantShop) error {
	m.Items = make([]domain.MerchantItem, 0)
	m.Sales = make([]domain.MerchantSale, 0)
	rows, err := q.QueryContext(ctx, `SELECT id,position,inv_type,template_id,bundles,per_bundle,price,equipment,expires_at,cash_id FROM asset_merchant_items WHERE shop_id=$1 ORDER BY position`, m.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item domain.MerchantItem
		var raw []byte
		if err = rows.Scan(&item.ID, &item.Position, &item.Item.InvType, &item.Item.TemplateID, &item.Bundles, &item.PerBundle, &item.Price, &raw, &item.Item.ExpiresAt, &item.Item.CashID); err != nil {
			rows.Close()
			return err
		}
		item.Item.Quantity = item.PerBundle
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &item.Equipment); err != nil {
				rows.Close()
				return err
			}
		}
		m.Items = append(m.Items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `SELECT template_id,bundles,per_bundle,total,buyer_id,buyer_name FROM (SELECT * FROM asset_merchant_sales WHERE shop_id=$1 ORDER BY id DESC LIMIT $2) recent ORDER BY id`, m.ID, merchantSalesShown)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sale domain.MerchantSale
		if err = rows.Scan(&sale.TemplateID, &sale.Bundles, &sale.PerBundle, &sale.Total, &sale.BuyerID, &sale.Buyer); err != nil {
			return err
		}
		m.Sales = append(m.Sales, sale)
	}
	return rows.Err()
}

func (s *Store) MerchantFor(ctx context.Context, accountID int64, worldID int) (domain.MerchantShop, bool, error) {
	m, err := scanMerchant(s.db.QueryRowContext(ctx, `SELECT `+merchantColumns+` FROM asset_merchant_shops WHERE account_id=$1 AND world_id=$2`, accountID, worldID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MerchantShop{}, false, nil
	}
	if err != nil {
		return domain.MerchantShop{}, false, err
	}
	if err = readMerchantDetail(ctx, s.db, &m); err != nil {
		return domain.MerchantShop{}, false, err
	}
	return m, true, nil
}

func (s *Store) ActiveMerchants(ctx context.Context, worldID int) ([]domain.MerchantShop, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+merchantColumns+` FROM asset_merchant_shops WHERE world_id=$1 AND state<>'closed' ORDER BY id`, worldID)
	if err != nil {
		return nil, err
	}
	var shops []domain.MerchantShop
	for rows.Next() {
		m, err := scanMerchant(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		shops = append(shops, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range shops {
		if err = readMerchantDetail(ctx, s.db, &shops[i]); err != nil {
			return nil, err
		}
	}
	return shops, nil
}

// CreateMerchant opens a shop in setup state at the owner's live position.
// The permit is checked, not consumed: it is a timed cash item (Legacy).
func (s *Store) CreateMerchant(ctx context.Context, r domain.MerchantCreate) (domain.MerchantShop, error) {
	title := r.Title
	if title == "" || utf8.RuneCountInString(title) > 40 || len(title) > 64 || r.PermitItemID/10000 != 503 || r.Session.ChannelID < 1 {
		return domain.MerchantShop{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MerchantShop{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.MerchantShop{}, err
	}
	permit := false
	for _, item := range c.Items {
		if item.InvType == 5 && item.Slot == r.PermitSlot && item.TemplateID == r.PermitItemID && item.Quantity > 0 {
			if err = guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
				return domain.MerchantShop{}, err
			}
			permit = true
			break
		}
	}
	if !permit {
		return domain.MerchantShop{}, domain.ErrNotFound
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM asset_merchant_shops WHERE (account_id=$1 AND world_id=$2) OR character_id=$3 LIMIT 1`, c.AccountID, c.WorldID, c.ID).Scan(&existing)
	if err == nil {
		return domain.MerchantShop{}, domain.ErrAlreadyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.MerchantShop{}, err
	}
	live := r.Session.Character
	m, err := scanMerchant(tx.QueryRowContext(ctx, `INSERT INTO asset_merchant_shops(character_id,account_id,world_id,channel_id,map_id,x,y,foothold,permit_item_id,owner_name,title,state,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'setup',clock_timestamp()+$12*interval '1 second') RETURNING `+merchantColumns,
		c.ID, c.AccountID, c.WorldID, r.Session.ChannelID, live.MapID, live.X, live.Y, r.Foothold, r.PermitItemID, c.Name, title, int64(domain.MerchantDuration/time.Second)))
	if err != nil {
		return domain.MerchantShop{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.MerchantShop{}, err
	}
	m.Items, m.Sales = make([]domain.MerchantItem, 0), make([]domain.MerchantSale, 0)
	return m, nil
}

func (s *Store) SetMerchantState(ctx context.Context, shopID int64, from []string, to string) (domain.MerchantShop, bool, error) {
	switch to {
	case domain.MerchantOpen, domain.MerchantMaintenance, domain.MerchantClosed:
	default:
		return domain.MerchantShop{}, false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MerchantShop{}, false, err
	}
	defer tx.Rollback()
	m, err := scanMerchant(tx.QueryRowContext(ctx, `SELECT `+merchantColumns+` FROM asset_merchant_shops WHERE id=$1 FOR UPDATE`, shopID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MerchantShop{}, false, domain.ErrNotFound
	}
	if err != nil {
		return domain.MerchantShop{}, false, err
	}
	allowed := false
	for _, state := range from {
		allowed = allowed || state == m.State
	}
	if !allowed {
		return domain.MerchantShop{}, false, domain.ErrStaleOwner
	}
	if err = readMerchantDetail(ctx, tx, &m); err != nil {
		return domain.MerchantShop{}, false, err
	}
	if to == domain.MerchantClosed && m.Mesos == 0 && !m.Stock() {
		// Nothing to hand back: no Fredrick package is left behind.
		if _, err = tx.ExecContext(ctx, `DELETE FROM asset_merchant_shops WHERE id=$1`, m.ID); err != nil {
			return domain.MerchantShop{}, false, err
		}
		return m, false, tx.Commit()
	}
	m.State = to
	m.Revision++
	if to == domain.MerchantClosed {
		if _, err = tx.ExecContext(ctx, `DELETE FROM asset_merchant_items WHERE shop_id=$1 AND bundles=0`, m.ID); err != nil {
			return domain.MerchantShop{}, false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE asset_merchant_shops SET state=$1,revision=$2,closed_at=clock_timestamp() WHERE id=$3`, to, m.Revision, m.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE asset_merchant_shops SET state=$1,revision=$2 WHERE id=$3`, to, m.Revision, m.ID)
	}
	if err != nil {
		return domain.MerchantShop{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return domain.MerchantShop{}, false, err
	}
	return m, true, nil
}

// MerchantTax is the seller's fee on one sale. The table is the
// merchant_fee_rates setting; its default is Legacy
// GameConstants.EntrustedStoreTax, which the native shop dialog also shows.
func MerchantTax(total int64) int64 { return domain.MerchantFee(total) }

// merchantBalanceCap bounds what a character may hold after a merchant
// payout: the character balance contract (64-bit under long_money).
func merchantBalanceCap() int64 { return domain.MaxMoney() }

// ChangeMerchant runs one owner or buyer command under one SQL commit. The
// caller's character and then the shop row are locked, in that order for
// every command, so a buyer and the owner cannot deadlock.
func (s *Store) ChangeMerchant(ctx context.Context, r domain.MerchantRequest) (domain.MerchantResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MerchantResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.MerchantResult{}, err
	}
	m, err := scanMerchant(tx.QueryRowContext(ctx, `SELECT `+merchantColumns+` FROM asset_merchant_shops WHERE id=$1 FOR UPDATE`, r.ShopID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MerchantResult{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.MerchantResult{}, err
	}
	if err = readMerchantDetail(ctx, tx, &m); err != nil {
		return domain.MerchantResult{}, err
	}
	if c.Revision != r.Session.Character.Revision || m.Revision != r.ExpectedRevision {
		return domain.MerchantResult{}, domain.ErrStaleOwner
	}
	if m.WorldID != c.WorldID {
		return domain.MerchantResult{}, domain.ErrForbidden
	}
	owner := m.CharacterID == c.ID
	before := c
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	result := domain.MerchantResult{Inventory: domain.InventoryResult{Character: c}, Shop: m}
	switch r.Kind {
	case "put":
		if !owner || (m.State != domain.MerchantSetup && m.State != domain.MerchantMaintenance) {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		err = merchantPut(ctx, tx, r, &result)
	case "take":
		if !owner || (m.State != domain.MerchantSetup && m.State != domain.MerchantMaintenance) {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		err = merchantTake(ctx, tx, r, &result)
	case "buy":
		if owner || m.AccountID == c.AccountID || m.State != domain.MerchantOpen {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		err = merchantBuy(ctx, tx, r, &result)
	case "collect":
		if !owner || (m.State != domain.MerchantSetup && m.State != domain.MerchantMaintenance) {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		err = merchantCollect(ctx, tx, &result, false)
	case "reclaim":
		// A closed shop is the Fredrick package; a live one is the owner
		// closing the shop from inside it.
		if !owner || m.State == domain.MerchantOpen {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		err = merchantCollect(ctx, tx, &result, true)
	default:
		err = domain.ErrInvalidInput
	}
	if err != nil {
		return domain.MerchantResult{}, err
	}
	if result.Inventory.Character.Mesos < 0 || result.Inventory.Character.Mesos > merchantBalanceCap() {
		return domain.MerchantResult{}, domain.ErrInsufficient
	}
	result.Shop.Revision++
	if result.Deleted {
		if _, err = tx.ExecContext(ctx, `DELETE FROM asset_merchant_shops WHERE id=$1`, m.ID); err != nil {
			return domain.MerchantResult{}, err
		}
	} else if _, err = tx.ExecContext(ctx, `UPDATE asset_merchant_shops SET mesos=$1,revision=$2 WHERE id=$3`, result.Shop.Mesos, result.Shop.Revision, m.ID); err != nil {
		return domain.MerchantResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &result.Inventory.Character); err != nil {
		return domain.MerchantResult{}, err
	}
	operation := fmt.Sprintf("merchant/%d/%d/%s/%d", m.ID, result.Shop.Revision, r.Kind, c.ID)
	if err = appendEconomicLedger(ctx, tx, operation, before, result.Inventory.Character, "merchant_"+r.Kind); err != nil {
		return domain.MerchantResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.MerchantResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}

func merchantPut(ctx context.Context, tx *sql.Tx, r domain.MerchantRequest, result *domain.MerchantResult) error {
	if r.Bundles < 1 || r.PerBundle < 1 || r.Price < 1 || r.Price > domain.MerchantMaxPrice() || r.InvType < 1 || r.InvType > 5 {
		return domain.ErrInvalidInput
	}
	if _, ok := domain.MerchantTotal(r.Price, r.Bundles); !ok {
		// Buying the whole row at once must not overflow 64 bits.
		return domain.ErrInvalidInput
	}
	quantity := r.Bundles * r.PerBundle
	if quantity > 30000 || (r.InvType == 1 && quantity != 1) {
		return domain.ErrInvalidInput
	}
	shop := &result.Shop
	if len(shop.Items) >= domain.MerchantItemLimit {
		return domain.ErrInsufficient
	}
	c := &result.Inventory.Character
	var instance, cashID int64
	template, stack := 0, 0
	if r.InvType == 1 {
		for _, e := range c.Equipment {
			if e.Slot == r.Slot {
				instance, cashID, template, stack = e.ID, e.CashID, e.ItemID, 1
			}
		}
	} else {
		for _, item := range c.Items {
			if item.InvType == r.InvType && item.Slot == r.Slot {
				instance, cashID, template, stack = item.ID, item.CashID, item.TemplateID, item.Quantity
			}
		}
	}
	if instance == 0 {
		return domain.ErrNotFound
	}
	cash := cashID > 0 || r.InvType == 5
	if cash {
		// Cash items are sold only where the operator allows it, and only as
		// a whole serial: never pets or shop permits, and no split stacks.
		definition, err := inventoryDefinition(template)
		if err != nil || !domain.MerchantCashItems() || cashID == 0 || definition.TradeBlock || definition.Quest || template/10000 == 500 || template/10000 == 503 {
			return domain.ErrForbidden
		}
		if r.Bundles != 1 || quantity != stack {
			return domain.ErrInvalidInput
		}
	}
	// Ordinary items must be tradeable: quest and trade-blocked ones stay out.
	taken, err := takeInventoryItems(ctx, tx, &result.Inventory, []domain.TradeItem{{InvType: r.InvType, Slot: r.Slot, Quantity: quantity, InstanceID: instance, TradeSlot: 1}}, !cash)
	if err != nil {
		return err
	}
	row := domain.MerchantItem{Bundles: r.Bundles, PerBundle: r.PerBundle, Price: r.Price, Equipment: taken[0].Equipment}
	if taken[0].Equipment != nil {
		row.Item = domain.InventoryItem{InvType: 1, TemplateID: taken[0].Equipment.ItemID, Quantity: 1, ExpiresAt: taken[0].Equipment.ExpiresAt, CashID: cashID}
	} else {
		row.Item = domain.InventoryItem{InvType: r.InvType, TemplateID: taken[0].Item.TemplateID, Quantity: r.PerBundle, ExpiresAt: taken[0].Item.ExpiresAt, CashID: cashID}
	}
	if err = guardItemExpiry(ctx, tx, row.Item.ExpiresAt); err != nil {
		return err
	}
	if cashID > 0 {
		updated, err := tx.ExecContext(ctx, `UPDATE asset_cash_items SET state='merchant',character_id=NULL,slot=0 WHERE id=$1 AND account_id=$2 AND state='inventory'`, cashID, c.AccountID)
		if err != nil {
			return err
		}
		if n, _ := updated.RowsAffected(); n != 1 {
			return domain.ErrStaleOwner
		}
	}
	for _, item := range shop.Items {
		row.Position = max(row.Position, item.Position)
	}
	row.Position++
	var equipment []byte
	if row.Equipment != nil {
		equipment, _ = json.Marshal(row.Equipment)
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_merchant_items(shop_id,position,inv_type,template_id,bundles,per_bundle,price,equipment,expires_at,cash_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, shop.ID, row.Position, row.Item.InvType, row.Item.TemplateID, row.Bundles, row.PerBundle, row.Price, nullableJSON(equipment), row.Item.ExpiresAt, cashID).Scan(&row.ID); err != nil {
		return err
	}
	shop.Items = append(shop.Items, row)
	return nil
}

// merchantReceive puts lots of one shop row into the character's inventory.
func merchantReceive(ctx context.Context, tx *sql.Tx, inventory *domain.InventoryResult, row domain.MerchantItem, bundles int) error {
	if bundles < 1 {
		return nil
	}
	item := row.Item
	item.Quantity = bundles * row.PerBundle
	if err := receiveTradeItem(ctx, tx, inventory, domain.TradeItem{InvType: item.InvType, Quantity: item.Quantity, Equipment: row.Equipment, Item: &item}); err != nil {
		return err
	}
	if item.CashID == 0 {
		return nil
	}
	// The serial follows the item to whoever holds it now, buyer or owner.
	c := inventory.Character
	updated, err := tx.ExecContext(ctx, `UPDATE asset_cash_items SET state='inventory',character_id=$1,account_id=$2,world_id=$3 WHERE id=$4 AND state='merchant'`, c.ID, c.AccountID, c.WorldID, item.CashID)
	if err != nil {
		return err
	}
	if n, _ := updated.RowsAffected(); n != 1 {
		return domain.ErrStaleOwner
	}
	return nil
}

func merchantRow(shop *domain.MerchantShop, id int64) int {
	for i, item := range shop.Items {
		if item.ID == id {
			return i
		}
	}
	return -1
}

func merchantTake(ctx context.Context, tx *sql.Tx, r domain.MerchantRequest, result *domain.MerchantResult) error {
	shop := &result.Shop
	index := merchantRow(shop, r.ItemID)
	if index < 0 {
		return domain.ErrNotFound
	}
	if err := merchantReceive(ctx, tx, &result.Inventory, shop.Items[index], shop.Items[index].Bundles); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM asset_merchant_items WHERE id=$1`, shop.Items[index].ID); err != nil {
		return err
	}
	shop.Items = append(shop.Items[:index], shop.Items[index+1:]...)
	return nil
}

func merchantBuy(ctx context.Context, tx *sql.Tx, r domain.MerchantRequest, result *domain.MerchantResult) error {
	shop := &result.Shop
	index := merchantRow(shop, r.ItemID)
	if index < 0 {
		return domain.ErrNotFound
	}
	row := shop.Items[index]
	if r.Bundles < 1 || r.Bundles > row.Bundles {
		return domain.ErrNotFound
	}
	if r.Price != row.Price {
		// The buyer was shown another price; never charge an unseen one.
		return domain.ErrStaleOwner
	}
	total, ok := domain.MerchantTotal(row.Price, r.Bundles)
	if !ok || total > domain.MerchantMaxPrice() {
		return domain.ErrInvalidInput
	}
	c := &result.Inventory.Character
	if c.Mesos < total {
		return domain.ErrInsufficient
	}
	if shop.Mesos > math.MaxInt64-total {
		// The shop income is a 64-bit balance; it must never wrap.
		return domain.ErrForbidden
	}
	if err := guardItemExpiry(ctx, tx, row.Item.ExpiresAt); err != nil {
		return err
	}
	if err := merchantReceive(ctx, tx, &result.Inventory, row, r.Bundles); err != nil {
		return err
	}
	c.Mesos -= total
	shop.Mesos += total - MerchantTax(total)
	row.Bundles -= r.Bundles
	shop.Items[index] = row
	if _, err := tx.ExecContext(ctx, `UPDATE asset_merchant_items SET bundles=$1 WHERE id=$2`, row.Bundles, row.ID); err != nil {
		return err
	}
	sale := domain.MerchantSale{TemplateID: row.Item.TemplateID, Bundles: r.Bundles, PerBundle: row.PerBundle, Total: total, BuyerID: c.ID, Buyer: c.Name}
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_merchant_sales(shop_id,template_id,bundles,per_bundle,total,buyer_id,buyer_name) VALUES($1,$2,$3,$4,$5,$6,$7)`, shop.ID, sale.TemplateID, sale.Bundles, sale.PerBundle, sale.Total, sale.BuyerID, sale.Buyer); err != nil {
		return err
	}
	shop.Sales = append(shop.Sales, sale)
	if len(shop.Sales) > merchantSalesShown {
		shop.Sales = shop.Sales[len(shop.Sales)-merchantSalesShown:]
	}
	result.Sale = &sale
	return nil
}

// merchantCollect pays the income to the owner and drops sold-out rows. With
// everything set it also returns every unsold item and removes the shop.
func merchantCollect(ctx context.Context, tx *sql.Tx, result *domain.MerchantResult, everything bool) error {
	shop := &result.Shop
	kept := shop.Items[:0:0]
	for _, row := range shop.Items {
		if row.Bundles > 0 && !everything {
			kept = append(kept, row)
			continue
		}
		if row.Bundles > 0 {
			if err := merchantReceive(ctx, tx, &result.Inventory, row, row.Bundles); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM asset_merchant_items WHERE id=$1`, row.ID); err != nil {
			return err
		}
	}
	shop.Items = kept
	if !everything {
		for i := range shop.Items {
			if shop.Items[i].Position == i+1 {
				continue
			}
			shop.Items[i].Position = i + 1
			if _, err := tx.ExecContext(ctx, `UPDATE asset_merchant_items SET position=$1 WHERE id=$2`, i+1, shop.Items[i].ID); err != nil {
				return err
			}
		}
	}
	c := &result.Inventory.Character
	if shop.Mesos > merchantBalanceCap()-c.Mesos {
		return domain.ErrInsufficient
	}
	c.Mesos += shop.Mesos
	shop.Mesos = 0
	result.Deleted = everything
	return nil
}
