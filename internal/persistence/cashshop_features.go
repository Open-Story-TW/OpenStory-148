package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"golang.org/x/text/encoding/traditionalchinese"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.CashFeaturesRepository = (*Store)(nil)

func (s *Store) ReadCashWishlist(ctx context.Context, session domain.CashSession) ([10]int, error) {
	var list [10]int
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return list, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, session); err != nil {
		return list, err
	}
	var data []byte
	err = tx.QueryRowContext(ctx, `SELECT serials FROM asset_cash_wishlists WHERE character_id=$1`, session.Session.CharacterID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return list, nil
	}
	if err != nil {
		return list, err
	}
	err = json.Unmarshal(data, &list)
	return list, err
}

func cashFeatureHash(r domain.CashFeatureRequest) string {
	// Credentials authorize the request, but are never retained in receipts.
	r.PIN = ""
	r.Session = domain.CashSession{Session: domain.CharacterSession{AccountID: r.Session.Session.AccountID, CharacterID: r.Session.Session.CharacterID}}
	raw, _ := json.Marshal(r)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func checkCashPurchaseLimit(ctx context.Context, tx *sql.Tx, accountID int64, sn int) error {
	var totalLimit, accountLimit sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT total_limit,account_limit FROM asset_cash_limits WHERE sn=$1 FOR UPDATE`, sn).Scan(&totalLimit, &accountLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var total, account int64
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE account_id=$2) FROM (SELECT account_id FROM asset_cash_orders WHERE sn=$1 UNION ALL SELECT account_id FROM asset_cash_feature_operations WHERE result#>>'{Product,SN}'=$1::text) p`, sn, accountID).Scan(&total, &account)
	if err != nil {
		return err
	}
	if (totalLimit.Valid && total >= totalLimit.Int64) || (accountLimit.Valid && account >= accountLimit.Int64) {
		return domain.ErrCashLimit
	}
	return nil
}

func createCashCart(ctx context.Context, tx *sql.Tx, accountID int64, worldID, gender int, products []domain.CashProduct) ([]domain.CashItem, error) {
	if len(products) == 0 || len(products) > 100 {
		return nil, domain.ErrInvalidInput
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_cash_items WHERE account_id=$1 AND state='locker' AND (expires_at=0 OR expires_at>extract(epoch FROM clock_timestamp())*1000)`, accountID).Scan(&current); err != nil {
		return nil, err
	}
	if current+len(products) > 100 {
		return nil, errors.Join(domain.ErrCashLockerFull, domain.ErrInsufficient)
	}
	// Validate the whole cart before the first insert: a refused package member
	// must never leave its siblings behind in the locker.
	for _, p := range products {
		if p.Quantity < 1 || p.Quantity > 30000 || p.PeriodDays < 0 || p.PeriodDays > 36500 {
			return nil, domain.ErrForbidden
		}
		if p.Gender != 2 && p.Gender != gender {
			return nil, errCashGender
		}
		if _, err := inventoryDefinition(p.TemplateID); err != nil || p.TemplateID/10000 == 500 {
			return nil, domain.ErrInvalidInput
		}
	}
	items := make([]domain.CashItem, 0, len(products))
	for _, p := range products {
		definition, err := inventoryDefinition(p.TemplateID)
		if err != nil {
			return nil, domain.ErrInvalidInput
		}
		item := domain.CashItem{AccountID: accountID, SN: p.SN, TemplateID: p.TemplateID, Quantity: p.Quantity, InvType: definition.InvType, State: "locker", ExpiresAt: cashExpiry(p)}
		var raw []byte
		if definition.InvType == 1 {
			e := definition.Equipment
			item.Equipment = &e
			raw, _ = json.Marshal(e)
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,equipment,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'locker') RETURNING id`, accountID, worldID, p.SN, p.TemplateID, p.Quantity, definition.InvType, item.ExpiresAt, nullableJSON(raw)).Scan(&item.ID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) ChangeCashFeature(ctx context.Context, r domain.CashFeatureRequest) (domain.CashFeatureResult, error) {
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 {
		return domain.CashFeatureResult{}, domain.ErrInvalidInput
	}
	if r.Kind == "gift" || r.Kind == "gift_package" {
		if err := s.VerifyPIN(ctx, r.Session.Session.AccountID, r.PIN); err != nil {
			return domain.CashFeatureResult{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashFeatureResult{}, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, r.Session); err != nil {
		return domain.CashFeatureResult{}, err
	}
	wallet, err := readCashWalletSQL(ctx, tx, r.Session.Session.AccountID)
	if err != nil {
		return domain.CashFeatureResult{}, err
	}
	result := domain.CashFeatureResult{Wallet: wallet}
	var savedHash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_cash_feature_operations WHERE operation_id=$1`, r.OperationID).Scan(&savedHash, &raw)
	if err == nil {
		if savedHash != cashFeatureHash(r) {
			return result, domain.ErrForbidden
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
		result.Wallet = wallet
		result.Replayed = true
		result.Changes = nil
		if result.Character.ID > 0 {
			result.Character, err = readCharacterSQL(ctx, tx, r.Session.Session.CharacterID)
		}
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	switch r.Kind {
	case "wishlist":
		for _, sn := range r.Wishlist {
			if sn != 0 {
				if _, err = content.CashProduct(sn); err != nil {
					return result, domain.ErrInvalidInput
				}
			}
		}
		raw, _ = json.Marshal(r.Wishlist)
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_wishlists(character_id,serials) VALUES($1,$2) ON CONFLICT(character_id) DO UPDATE SET serials=EXCLUDED.serials`, r.Session.Session.CharacterID, raw); err != nil {
			return result, err
		}
		result.Wishlist = r.Wishlist
	case "package", "gift", "gift_package":
		p, e := content.CashProduct(r.SN)
		if e != nil || !p.OnSale || p.Price < 1 {
			return result, domain.ErrInvalidInput
		}
		if result.Wallet.Credit < p.Price {
			return result, domain.ErrInsufficient
		}
		if err = checkCashPurchaseLimit(ctx, tx, r.Session.Session.AccountID, p.SN); err != nil {
			return result, err
		}
		if err = checkNativeCashLimit(ctx, tx, r.Session.Session.AccountID, p); err != nil {
			return result, err
		}
		products := []domain.CashProduct{p}
		result.Product = p
		result.Package = r.Kind != "gift"
		if result.Package {
			products, err = content.CashPackage(p.TemplateID)
			if err != nil {
				return result, domain.ErrInvalidInput
			}
		}
		if r.Kind == "package" {
			if p.Gender != 2 && p.Gender != r.Session.Session.Character.Gender {
				return result, errCashGender
			}
			result.Items, err = createCashCart(ctx, tx, r.Session.Session.AccountID, r.Session.Session.Lease.WorldID, r.Session.Session.Character.Gender, products)
			if err != nil {
				return result, err
			}
		} else {
			message, e := traditionalchinese.Big5.NewEncoder().Bytes([]byte(r.Message))
			if e != nil || len(message) < 1 || len(message) > 73 || len(r.Recipient) > 32 {
				return result, domain.ErrInvalidInput
			}
			var recipient, account int64
			var gender int
			var name string
			if err = tx.QueryRowContext(ctx, `SELECT id,account_id,gender,name FROM game_characters WHERE lower(name)=lower($1) AND world_id=$2`, r.Recipient, r.Session.Session.Lease.WorldID).Scan(&recipient, &account, &gender, &name); err != nil {
				return result, domain.ErrNotFound
			}
			if account == r.Session.Session.AccountID {
				return result, errors.Join(domain.ErrCashSameAccount, domain.ErrForbidden)
			}
			if p.Gender != 2 && p.Gender != gender {
				return result, errCashGender
			}
			// Validate every package member before charging; delivery repeats this
			// validation inside the recipient's atomic claim.
			for _, p := range products {
				if _, e = inventoryDefinition(p.TemplateID); e != nil || p.TemplateID/10000 == 500 {
					return result, domain.ErrInvalidInput
				}
				if p.Gender != 2 && p.Gender != gender {
					return result, errCashGender
				}
			}
			var sender string
			if err = tx.QueryRowContext(ctx, `SELECT name FROM game_characters WHERE id=$1`, r.Session.Session.CharacterID).Scan(&sender); err != nil {
				return result, err
			}
			raw, _ = json.Marshal(products)
			if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_gifts(sender_account_id,sender_name,recipient_character_id,message,sn,template_id,products) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.Session.Session.AccountID, sender, recipient, r.Message, p.SN, p.TemplateID, raw); err != nil {
				return result, err
			}
			result.Recipient = name
		}
		result.Wallet.Credit -= p.Price
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_ledger(operation_id,account_id,currency,delta,balance_after) VALUES($1,$2,1,$3,$4)`, r.OperationID, r.Session.Session.AccountID, -p.Price, result.Wallet.Credit); err != nil {
			return result, err
		}
	case "coupon":
		if err = redeemCashCoupon(ctx, tx, r, &result); err != nil {
			return result, err
		}
	default:
		return result, domain.ErrInvalidInput
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=$1,points=$2,prepaid=$3 WHERE account_id=$4`, result.Wallet.Credit, result.Wallet.Points, result.Wallet.Prepaid, r.Session.Session.AccountID); err != nil {
		return result, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_feature_operations(operation_id,account_id,request_hash,result) VALUES($1,$2,$3,$4)`, r.OperationID, r.Session.Session.AccountID, cashFeatureHash(r), raw); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Store) ClaimCashGifts(ctx context.Context, session domain.CashSession) ([]domain.CashGift, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, session); err != nil {
		return nil, err
	}
	if _, err = readCashWalletSQL(ctx, tx, session.Session.AccountID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,sender_name,message,products,sn,template_id FROM asset_cash_gifts WHERE recipient_character_id=$1 AND delivered_at IS NULL ORDER BY id LIMIT 100 FOR UPDATE`, session.Session.CharacterID)
	if err != nil {
		return nil, err
	}
	type pendingGift struct {
		id              int64
		sender, message string
		products        []domain.CashProduct
		sn, templateID  int
	}
	var pending []pendingGift
	for rows.Next() {
		var g pendingGift
		var raw []byte
		if err = rows.Scan(&g.id, &g.sender, &g.message, &raw, &g.sn, &g.templateID); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &g.products); err != nil {
			break
		}
		pending = append(pending, g)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	gifts := make([]domain.CashGift, 0, len(pending))
	for _, g := range pending {
		items, e := createCashCart(ctx, tx, session.Session.AccountID, session.Session.Lease.WorldID, session.Session.Character.Gender, g.products)
		if e != nil {
			// A gift/package remains pending intact if it cannot fit. Earlier
			// complete gifts may still be delivered; no package is split.
			if errors.Is(e, domain.ErrInsufficient) {
				break
			}
			// A gift that no longer validates (catalogue change, recipient gender
			// change) stays pending instead of blocking every cash shop entry.
			// createCashCart validates before inserting, so nothing is partial.
			if errors.Is(e, domain.ErrForbidden) || errors.Is(e, domain.ErrInvalidInput) {
				continue
			}
			return nil, e
		}
		for i := range items {
			items[i].GiftFrom = g.sender
			if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_items SET gift_from=$1 WHERE id=$2`, g.sender, items[i].ID); err != nil {
				return nil, err
			}
		}
		notification := items[0]
		notification.SN, notification.TemplateID = g.sn, g.templateID
		gifts = append(gifts, domain.CashGift{Item: notification, Sender: g.sender, Message: g.message})
		if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_gifts SET delivered_at=clock_timestamp() WHERE id=$1`, g.id); err != nil {
			return nil, err
		}
	}
	return gifts, tx.Commit()
}

func redeemCashCoupon(ctx context.Context, tx *sql.Tx, r domain.CashFeatureRequest, result *domain.CashFeatureResult) error {
	code := strings.TrimSpace(r.Coupon)
	if len(code) < 1 || len(code) > 64 {
		return domain.ErrInvalidInput
	}
	var kind string
	var value int64
	var bound, used sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT kind,value,account_id,redeemed_account_id FROM asset_cash_coupons WHERE code_hash=$1 AND expires_at>clock_timestamp() FOR UPDATE`, tokenHash(code)).Scan(&kind, &value, &bound, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if used.Valid {
		return domain.ErrAlreadyExists
	}
	if bound.Valid && bound.Int64 != r.Session.Session.AccountID {
		return domain.ErrForbidden
	}
	switch kind {
	case "credit", "points":
		currency := 1
		balance := &result.Wallet.Credit
		if kind == "points" {
			currency = 2
			balance = &result.Wallet.Points
		}
		if value > domain.MaxMoney()-*balance {
			return domain.ErrInsufficient
		}
		*balance += value
		result.CreditAward = value
		if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_ledger(operation_id,account_id,currency,delta,balance_after) VALUES($1,$2,$3,$4,$5)`, r.OperationID, r.Session.Session.AccountID, currency, value, *balance); err != nil {
			return err
		}
	case "item":
		p, e := content.CashProduct(int(value))
		if e != nil {
			return domain.ErrInvalidInput
		}
		items, e := createCashCart(ctx, tx, r.Session.Session.AccountID, r.Session.Session.Lease.WorldID, r.Session.Session.Character.Gender, []domain.CashProduct{p})
		if e != nil {
			return e
		}
		var cid int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 FOR UPDATE`, r.Session.Session.CharacterID).Scan(&cid); err != nil {
			return err
		}
		c, e := readCharacterSQL(ctx, tx, cid)
		if e != nil {
			return e
		}
		inventory := domain.InventoryResult{Character: c}
		item := &items[0]
		if item.InvType == 1 {
			eq := *item.Equipment
			eq.CashID = item.ID
			eq.ExpiresAt = item.ExpiresAt
			eq.Slot = FreeEquipmentSlot(&c)
			if eq.Slot == 0 {
				return domain.ErrInsufficient
			}
			if err = insertEquipment(ctx, tx, c.ID, &eq); err != nil {
				return err
			}
			inventory.Character.Equipment = append(inventory.Character.Equipment, eq)
			inventory.Changes = []domain.InventoryChange{{Mode: 0, InvType: 1, Slot: eq.Slot, Equipment: &eq}}
			item.Slot = eq.Slot
			item.Equipment = &eq
		} else {
			e := domain.InventoryItem{CashID: item.ID, ExpiresAt: item.ExpiresAt, TemplateID: item.TemplateID, InvType: item.InvType, Quantity: item.Quantity}
			if err = receiveCashBundle(ctx, tx, &inventory, e); err != nil {
				return err
			}
			item.Slot = inventory.Changes[0].Slot
		}
		item.State = "inventory"
		item.CharacterID = c.ID
		if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_items SET state='inventory',character_id=$1,slot=$2 WHERE id=$3`, c.ID, item.Slot, item.ID); err != nil {
			return err
		}
		if err = commitInventoryState(ctx, tx, &inventory.Character); err != nil {
			return err
		}
		result.Character = inventory.Character
		result.Changes = inventory.Changes
		result.Items = items
	case "mesos":
		var cid int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 FOR UPDATE`, r.Session.Session.CharacterID).Scan(&cid); err != nil {
			return err
		}
		c, e := readCharacterSQL(ctx, tx, cid)
		if e != nil {
			return e
		}
		if value > domain.MaxMoney()-c.Mesos {
			return domain.ErrInsufficient
		}
		before := c
		c.Mesos += value
		if err = commitInventoryState(ctx, tx, &c); err != nil {
			return err
		}
		if err = appendEconomicLedger(ctx, tx, r.OperationID, before, c, "cash_coupon"); err != nil {
			return err
		}
		result.Character = c
		result.MesosAward = value
	default:
		return domain.ErrInvalidInput
	}
	_, err = tx.ExecContext(ctx, `UPDATE asset_cash_coupons SET redeemed_account_id=$1,redeemed_operation_id=$2,redeemed_at=clock_timestamp() WHERE code_hash=$3`, r.Session.Session.AccountID, r.OperationID, tokenHash(code))
	return err
}
