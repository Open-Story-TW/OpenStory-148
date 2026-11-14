package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.CashRepository = (*Store)(nil)

func readCashWalletSQL(ctx context.Context, tx *sql.Tx, accountID int64) (domain.CashWallet, error) {
	var w domain.CashWallet
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_cash_wallets(account_id) VALUES($1) ON CONFLICT DO NOTHING`, accountID); err != nil {
		return w, err
	}
	err := tx.QueryRowContext(ctx, `SELECT credit,points,prepaid FROM asset_cash_wallets WHERE account_id=$1 FOR UPDATE`, accountID).Scan(&w.Credit, &w.Points, &w.Prepaid)
	return w, err
}
func (s *Store) ReadCashWallet(ctx context.Context, c domain.CashSession) (domain.CashWallet, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashWallet{}, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, c); err != nil {
		return domain.CashWallet{}, err
	}
	w, err := readCashWalletSQL(ctx, tx, c.Session.AccountID)
	if err != nil {
		return w, err
	}
	return w, tx.Commit()
}

func scanCashItem(row interface{ Scan(...any) error }) (domain.CashItem, error) {
	var item domain.CashItem
	var raw []byte
	var cid sql.NullInt64
	err := row.Scan(&item.ID, &item.AccountID, &cid, &item.SN, &item.TemplateID, &item.Quantity, &item.InvType, &item.Slot, &item.ExpiresAt, &item.State, &raw, &item.GiftFrom)
	item.CharacterID = cid.Int64
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &item.Equipment)
	}
	return item, err
}

func (s *Store) ReadCashLocker(ctx context.Context, c domain.CashSession) ([]domain.CashItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, c); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,account_id,character_id,sn,template_id,quantity,inv_type,slot,expires_at,state,equipment,gift_from FROM asset_cash_items WHERE account_id=$1 AND state='locker' AND (expires_at=0 OR expires_at>$2) ORDER BY id LIMIT 100`, c.Session.AccountID, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.CashItem, 0)
	for rows.Next() {
		item, e := scanCashItem(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) PurchaseCash(ctx context.Context, r domain.CashPurchaseRequest) (domain.CashPurchaseResult, error) {
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 || !(r.Currency == 1 || r.Currency == 2 || r.Currency == 4) || r.Option != 0 {
		return domain.CashPurchaseResult{}, domain.ErrInvalidInput
	}
	product, err := content.CashProduct(r.SN)
	if err != nil || !product.OnSale || product.Price < 1 || product.Price > maxMesos || product.Quantity < 1 || product.Quantity > 30000 {
		return domain.CashPurchaseResult{}, domain.ErrInvalidInput
	}
	// Native Limit2 first-purchase offers use credit only (4755DD).
	if product.LimitMode == 2 && r.Currency != 1 {
		return domain.CashPurchaseResult{}, domain.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashPurchaseResult{}, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, r.Session); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	wallet, err := readCashWalletSQL(ctx, tx, r.Session.Session.AccountID)
	if err != nil {
		return domain.CashPurchaseResult{}, err
	}
	var sn, currency int
	var aid int64
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT account_id,sn,currency,result FROM asset_cash_orders WHERE operation_id=$1`, r.OperationID).Scan(&aid, &sn, &currency, &raw)
	if err == nil {
		if aid != r.Session.Session.AccountID || sn != r.SN || currency != r.Currency {
			return domain.CashPurchaseResult{}, domain.ErrForbidden
		}
		var result domain.CashPurchaseResult
		if err = json.Unmarshal(raw, &result); err != nil {
			return domain.CashPurchaseResult{}, err
		}
		result.Wallet = wallet
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.CashPurchaseResult{}, err
	}
	if err = checkCashPurchaseLimit(ctx, tx, r.Session.Session.AccountID, r.SN); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	if err = checkNativeCashLimit(ctx, tx, r.Session.Session.AccountID, product); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	if product.Gender != 2 && product.Gender != r.Session.Session.Character.Gender {
		return domain.CashPurchaseResult{}, errCashGender
	}
	definition, err := inventoryDefinition(product.TemplateID)
	if err != nil {
		return domain.CashPurchaseResult{}, domain.ErrInvalidInput
	}
	var balance *int64
	switch r.Currency {
	case 1:
		balance = &wallet.Credit
	case 2:
		balance = &wallet.Points
	case 4:
		balance = &wallet.Prepaid
	}
	if *balance < product.Price {
		return domain.CashPurchaseResult{}, domain.ErrInsufficient
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_cash_items WHERE account_id=$1 AND state='locker' AND (expires_at=0 OR expires_at>$2)`, r.Session.Session.AccountID, time.Now().UnixMilli()).Scan(&count); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	if count >= 100 {
		return domain.CashPurchaseResult{}, errors.Join(domain.ErrCashLockerFull, domain.ErrInsufficient)
	}
	item := domain.CashItem{AccountID: r.Session.Session.AccountID, SN: product.SN, TemplateID: product.TemplateID, Quantity: product.Quantity, InvType: definition.InvType, State: "locker"}
	item.ExpiresAt = cashExpiry(product)
	if days := petLifeDays(product.TemplateID, product.PeriodDays); days > 0 {
		item.ExpiresAt = time.Now().Add(time.Duration(days) * 24 * time.Hour).UnixMilli()
	}
	var eq []byte
	if definition.InvType == 1 {
		e := definition.Equipment
		item.Equipment = &e
		eq, _ = json.Marshal(e)
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,equipment,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'locker') RETURNING id`, item.AccountID, r.Session.Session.Lease.WorldID, item.SN, item.TemplateID, item.Quantity, item.InvType, item.ExpiresAt, nullableJSON(eq)).Scan(&item.ID); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	// Gifts and packages still refuse pets; a direct purchase gets pet state.
	if domain.IsPetTemplate(item.TemplateID) {
		if err = createCashPet(ctx, tx, item.ID, item.TemplateID); err != nil {
			return domain.CashPurchaseResult{}, err
		}
	}
	*balance -= product.Price
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=$1,points=$2,prepaid=$3 WHERE account_id=$4`, wallet.Credit, wallet.Points, wallet.Prepaid, item.AccountID); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_ledger(operation_id,account_id,currency,delta,balance_after) VALUES($1,$2,$3,$4,$5)`, r.OperationID, item.AccountID, r.Currency, -product.Price, *balance); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	result := domain.CashPurchaseResult{Item: item, Wallet: wallet}
	raw, err = json.Marshal(result)
	if err != nil {
		return domain.CashPurchaseResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_orders(operation_id,account_id,character_id,sn,currency,price,item_id,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.OperationID, item.AccountID, r.Session.Session.CharacterID, r.SN, r.Currency, product.Price, item.ID, raw); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.CashPurchaseResult{}, err
	}
	return result, nil
}

func (s *Store) MoveCashItem(ctx context.Context, r domain.CashMoveRequest) (domain.CashMoveResult, error) {
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 || r.CashID < 1 || r.InvType < 0 || r.InvType > 5 || r.TargetSlot < 0 || r.TargetSlot > domain.MaxInventorySlots || r.ToLocker && r.TargetSlot != 0 {
		return domain.CashMoveResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashMoveResult{}, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, r.Session); err != nil {
		return domain.CashMoveResult{}, err
	}
	var cid int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 FOR UPDATE`, r.Session.Session.CharacterID).Scan(&cid); err != nil {
		return domain.CashMoveResult{}, err
	}
	c, err := readCharacterSQL(ctx, tx, cid)
	if err != nil {
		return domain.CashMoveResult{}, err
	}
	var aid, cashID int64
	var toLocker bool
	var invType, targetSlot int
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT account_id,cash_id,to_locker,inventory_type,target_slot,result FROM asset_cash_moves WHERE operation_id=$1`, r.OperationID).Scan(&aid, &cashID, &toLocker, &invType, &targetSlot, &raw)
	if err == nil {
		if aid != r.Session.Session.AccountID || cashID != r.CashID || toLocker != r.ToLocker || invType != r.InvType || targetSlot != r.TargetSlot {
			return domain.CashMoveResult{}, domain.ErrForbidden
		}
		var result domain.CashMoveResult
		if err = json.Unmarshal(raw, &result); err != nil {
			return domain.CashMoveResult{}, err
		}
		result.Character = c
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.CashMoveResult{}, err
	}
	item, err := scanCashItem(tx.QueryRowContext(ctx, `SELECT id,account_id,character_id,sn,template_id,quantity,inv_type,slot,expires_at,state,equipment,gift_from FROM asset_cash_items WHERE id=$1 AND account_id=$2 FOR UPDATE`, r.CashID, r.Session.Session.AccountID))
	if err != nil {
		return domain.CashMoveResult{}, domain.ErrNotFound
	}
	if err = guardItemExpiry(ctx, tx, item.ExpiresAt); err != nil {
		return domain.CashMoveResult{}, err
	}
	if r.InvType != 0 && r.InvType != item.InvType {
		return domain.CashMoveResult{}, domain.ErrForbidden
	}
	if r.ToLocker {
		if item.State != "inventory" || item.CharacterID != c.ID || item.Slot < 1 {
			return domain.CashMoveResult{}, domain.ErrForbidden
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_cash_items WHERE account_id=$1 AND state='locker' AND (expires_at=0 OR expires_at>$2)`, item.AccountID, time.Now().UnixMilli()).Scan(&count); err != nil {
			return domain.CashMoveResult{}, err
		}
		if count >= 100 {
			return domain.CashMoveResult{}, errors.Join(domain.ErrCashLockerFull, domain.ErrInsufficient)
		}
		if item.InvType == 1 {
			index := -1
			for i, e := range c.Equipment {
				if e.Slot == item.Slot && e.CashID == item.ID {
					index = i
					break
				}
			}
			if index < 0 {
				return domain.CashMoveResult{}, domain.ErrNotFound
			}
			e := c.Equipment[index]
			item.Quantity = 1
			item.Equipment = &e
			if _, err = tx.ExecContext(ctx, `DELETE FROM asset_equipment WHERE id=$1`, e.ID); err != nil {
				return domain.CashMoveResult{}, err
			}
			c.Equipment = append(c.Equipment[:index], c.Equipment[index+1:]...)
		} else {
			index := -1
			for i, e := range c.Items {
				if e.Slot == item.Slot && e.CashID == item.ID {
					index = i
					break
				}
			}
			if index < 0 {
				return domain.CashMoveResult{}, domain.ErrNotFound
			}
			item.Quantity = c.Items[index].Quantity
			if _, err = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, c.Items[index].ID); err != nil {
				return domain.CashMoveResult{}, err
			}
			c.Items = append(c.Items[:index], c.Items[index+1:]...)
		}
		item.State = "locker"
		item.Slot = 0
		item.CharacterID = 0
	} else {
		if item.State != "locker" {
			return domain.CashMoveResult{}, domain.ErrForbidden
		}
		slot := r.TargetSlot
		if slot != 0 {
			if slot > c.Capacity(item.InvType) {
				return domain.CashMoveResult{}, domain.ErrForbidden
			}
			for _, e := range c.Equipment {
				if item.InvType == 1 && e.Slot == slot {
					return domain.CashMoveResult{}, domain.ErrInsufficient
				}
			}
			for _, e := range c.Items {
				if e.InvType == item.InvType && e.Slot == slot {
					return domain.CashMoveResult{}, domain.ErrInsufficient
				}
			}
		} else if item.InvType == 1 {
			slot = FreeEquipmentSlot(&c)
		} else {
			for n := 1; n <= c.Capacity(item.InvType); n++ {
				free := true
				for _, existing := range c.Items {
					if existing.InvType == item.InvType && existing.Slot == n {
						free = false
						break
					}
				}
				if free {
					slot = n
					break
				}
			}
		}
		if slot == 0 {
			return domain.CashMoveResult{}, domain.ErrInsufficient
		}
		if item.InvType == 1 {
			if item.Equipment == nil {
				return domain.CashMoveResult{}, domain.ErrInvalidInput
			}
			e := *item.Equipment
			e.CashID = item.ID
			e.ExpiresAt = item.ExpiresAt
			e.Slot = slot
			if err = insertEquipment(ctx, tx, c.ID, &e); err != nil {
				return domain.CashMoveResult{}, err
			}
			c.Equipment = append(c.Equipment, e)
			item.Equipment = &e
		} else {
			e := domain.InventoryItem{CashID: item.ID, ExpiresAt: item.ExpiresAt, TemplateID: item.TemplateID, InvType: item.InvType, Slot: slot, Quantity: item.Quantity, Revision: 1}
			if err = tx.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,$2,$3,$4,$5) RETURNING id`, c.ID, item.InvType, slot, item.TemplateID, item.Quantity).Scan(&e.ID); err != nil {
				return domain.CashMoveResult{}, err
			}
			c.Items = append(c.Items, e)
		}
		item.State = "inventory"
		item.Slot = slot
		item.CharacterID = c.ID
	}
	if item.Equipment != nil {
		raw, _ = json.Marshal(item.Equipment)
	} else {
		raw = nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_items SET state=$1,slot=$2,character_id=$3,equipment=$4,quantity=$6,world_id=$7 WHERE id=$5`, item.State, item.Slot, func() any {
		if item.CharacterID == 0 {
			return nil
		}
		return item.CharacterID
	}(), nullableJSON(raw), item.ID, item.Quantity, c.WorldID); err != nil {
		return domain.CashMoveResult{}, err
	}
	if domain.IsPetTemplate(item.TemplateID) {
		// A pet in the locker is never summoned; its CASH slot is reloaded.
		if r.ToLocker {
			if _, err = tx.ExecContext(ctx, `UPDATE asset_pets SET summoned_by=NULL,summon_index=NULL WHERE cash_id=$1`, item.ID); err != nil {
				return domain.CashMoveResult{}, err
			}
		}
		if err = readPets(ctx, tx, &c); err != nil {
			return domain.CashMoveResult{}, err
		}
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		return domain.CashMoveResult{}, err
	}
	result := domain.CashMoveResult{Item: item, Character: c}
	raw, err = json.Marshal(result)
	if err != nil {
		return domain.CashMoveResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_moves(operation_id,account_id,character_id,cash_id,to_locker,inventory_type,target_slot,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.OperationID, item.AccountID, c.ID, item.ID, r.ToLocker, r.InvType, r.TargetSlot, raw); err != nil {
		return domain.CashMoveResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.CashMoveResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}
