package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"maple-go-server/internal/domain"
)

var _ domain.StorageRepository = (*Store)(nil)

func readStorageSQL(ctx context.Context, tx *sql.Tx, accountID int64, worldID int) (domain.Storage, error) {
	value := domain.Storage{AccountID: accountID, WorldID: worldID, Items: make([]domain.StoredItem, 0)}
	if err := tx.QueryRowContext(ctx, `SELECT capacity,mesos,revision FROM asset_storages WHERE account_id=$1 AND world_id=$2 FOR UPDATE`, accountID, worldID).Scan(&value.Capacity, &value.Mesos, &value.Revision); err != nil {
		return value, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,position,inv_type,template_id,quantity,equipment,cash_id,expires_at FROM asset_storage_items WHERE account_id=$1 AND world_id=$2 ORDER BY position`, accountID, worldID)
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.StoredItem
		var raw []byte
		if err = rows.Scan(&item.ID, &item.Position, &item.Item.InvType, &item.Item.TemplateID, &item.Item.Quantity, &raw, &item.Item.CashID, &item.Item.ExpiresAt); err != nil {
			return value, err
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &item.Equipment); err != nil {
				return value, err
			}
		}
		value.Items = append(value.Items, item)
	}
	return value, rows.Err()
}

func ensureStorage(ctx context.Context, tx *sql.Tx, accountID int64, worldID int) (domain.Storage, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO asset_storages(account_id,world_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, accountID, worldID); err != nil {
		return domain.Storage{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM asset_storages WHERE account_id=$1 AND world_id=$2 FOR UPDATE`, accountID, worldID); err != nil {
		return domain.Storage{}, err
	}
	if err := expireStoredCash(ctx, tx, accountID, worldID); err != nil {
		return domain.Storage{}, err
	}
	return readStorageSQL(ctx, tx, accountID, worldID)
}

func (s *Store) OpenStorage(ctx context.Context, session domain.CharacterSession) (domain.Storage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Storage{}, err
	}
	defer tx.Rollback()
	if _, err = lockInventoryCharacter(ctx, tx, session); err != nil {
		return domain.Storage{}, err
	}
	storage, err := ensureStorage(ctx, tx, session.AccountID, session.Lease.WorldID)
	if err != nil {
		return domain.Storage{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Storage{}, err
	}
	return storage, nil
}

func storageHash(r domain.StorageRequest) string {
	value := struct {
		AccountID, CharacterID         int64
		Session                        string
		Generation                     int64
		Kind                           string
		Slot, TemplateID, Quantity     int
		StorageItemID, Delta, Revision int64
	}{r.Session.AccountID, r.Session.CharacterID, r.Session.SessionID, r.Session.Generation, r.Kind, r.Slot, r.TemplateID, r.Quantity, r.StorageItemID, r.Delta, r.ExpectedRevision}
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) ChangeStorage(ctx context.Context, r domain.StorageRequest) (domain.StorageResult, error) {
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 {
		return domain.StorageResult{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.StorageResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, r.Session)
	if err != nil {
		return domain.StorageResult{}, err
	}
	storage, err := ensureStorage(ctx, tx, c.AccountID, c.WorldID)
	if err != nil {
		return domain.StorageResult{}, err
	}
	var hash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_storage_operations WHERE operation_id=$1`, r.OperationID).Scan(&hash, &raw)
	if err == nil {
		if hash != storageHash(r) {
			return domain.StorageResult{}, domain.ErrForbidden
		}
		var result domain.StorageResult
		if err = json.Unmarshal(raw, &result); err != nil {
			return domain.StorageResult{}, err
		}
		result.Inventory.Character = c
		result.Inventory.Changes = nil
		result.Storage = storage
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.StorageResult{}, err
	}
	if c.Revision != r.Session.Character.Revision || storage.Revision != r.ExpectedRevision {
		return domain.StorageResult{}, domain.ErrStaleOwner
	}
	c.HP, c.MP = r.Session.Character.HP, r.Session.Character.MP
	result := domain.StorageResult{Inventory: domain.InventoryResult{Character: c}, Storage: storage}
	err = nil
	switch r.Kind {
	case "deposit":
		err = depositStorage(ctx, tx, r, &result)
	case "withdraw":
		err = withdrawStorage(ctx, tx, r, &result)
	case "mesos":
		if r.Delta == 0 || r.Delta < -domain.MaxMoney() || r.Delta > domain.MaxMoney() {
			err = domain.ErrInvalidInput
			break
		}
		nextStorage, nextCharacter := storage.Mesos-r.Delta, c.Mesos+r.Delta
		if nextStorage < 0 || nextStorage > domain.MaxMoney() || nextCharacter < 0 || nextCharacter > domain.MaxMoney() {
			err = domain.ErrInsufficient
			break
		}
		result.Storage.Mesos = nextStorage
		result.Inventory.Character.Mesos = nextCharacter
	case "arrange":
		sort.SliceStable(result.Storage.Items, func(i, j int) bool {
			a, b := result.Storage.Items[i], result.Storage.Items[j]
			if a.Item.InvType != b.Item.InvType {
				return a.Item.InvType < b.Item.InvType
			}
			if a.Item.TemplateID != b.Item.TemplateID {
				return a.Item.TemplateID < b.Item.TemplateID
			}
			return a.ID < b.ID
		})
		for i := range result.Storage.Items {
			result.Storage.Items[i].Position = i + 1
			if _, err = tx.ExecContext(ctx, `UPDATE asset_storage_items SET position=$1 WHERE id=$2`, i+1, result.Storage.Items[i].ID); err != nil {
				break
			}
		}
	default:
		err = domain.ErrInvalidInput
	}
	if err != nil {
		return domain.StorageResult{}, err
	}
	result.Storage.Revision++
	if _, err = tx.ExecContext(ctx, `UPDATE asset_storages SET mesos=$1,revision=$2 WHERE account_id=$3 AND world_id=$4`, result.Storage.Mesos, result.Storage.Revision, c.AccountID, c.WorldID); err != nil {
		return domain.StorageResult{}, err
	}
	if err = commitInventoryState(ctx, tx, &result.Inventory.Character); err != nil {
		return domain.StorageResult{}, err
	}
	if err = appendEconomicLedger(ctx, tx, "storage/"+r.OperationID, c, result.Inventory.Character, "storage_"+r.Kind); err != nil {
		return domain.StorageResult{}, err
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return domain.StorageResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_storage_operations(operation_id,account_id,world_id,character_id,request_hash,result) VALUES($1,$2,$3,$4,$5,$6)`, r.OperationID, c.AccountID, c.WorldID, c.ID, storageHash(r), raw); err != nil {
		return domain.StorageResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.StorageResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	return result, nil
}

func appendEconomicLedger(ctx context.Context, tx *sql.Tx, operationID string, before, after domain.Character, reason string) error {
	if before.Mesos == after.Mesos {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO asset_meso_ledger(operation_id,character_id,account_id,world_id,delta,balance_after,reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, operationID, after.ID, after.AccountID, after.WorldID, after.Mesos-before.Mesos, after.Mesos, reason)
	return err
}

func depositStorage(ctx context.Context, tx *sql.Tx, r domain.StorageRequest, result *domain.StorageResult) error {
	c := &result.Inventory.Character
	if r.Slot < 1 || r.Slot > c.Capacity(r.TemplateID/1000000) || r.Quantity < 1 || r.Quantity > 30000 || r.TemplateID < 1000000 || r.TemplateID >= 6000000 || r.TemplateID/10000 == 500 {
		return domain.ErrInvalidInput
	}
	if len(result.Storage.Items) >= result.Storage.Capacity || c.Mesos < 100 {
		return domain.ErrInsufficient
	}
	stored := domain.StoredItem{Item: domain.InventoryItem{InvType: r.TemplateID / 1000000, TemplateID: r.TemplateID, Quantity: r.Quantity}}
	for pos := 1; pos <= result.Storage.Capacity; pos++ {
		free := true
		for _, item := range result.Storage.Items {
			if item.Position == pos {
				free = false
				break
			}
		}
		if free {
			stored.Position = pos
			break
		}
	}
	if stored.Item.InvType == 1 {
		index := -1
		for i, e := range c.Equipment {
			if e.Slot == r.Slot && e.ItemID == r.TemplateID {
				index = i
				break
			}
		}
		if index < 0 || r.Quantity != 1 {
			return domain.ErrNotFound
		}
		e := c.Equipment[index]
		stored.Item.CashID, stored.Item.ExpiresAt = e.CashID, e.ExpiresAt
		stored.Equipment = &e
		if _, err := tx.ExecContext(ctx, `DELETE FROM asset_equipment WHERE id=$1`, e.ID); err != nil {
			return err
		}
		c.Equipment = append(c.Equipment[:index], c.Equipment[index+1:]...)
		result.Inventory.Changes = []domain.InventoryChange{{Mode: 3, InvType: 1, Slot: r.Slot}}
	} else {
		index := -1
		for i, item := range c.Items {
			if item.Slot == r.Slot && item.InvType == stored.Item.InvType && item.TemplateID == r.TemplateID {
				index = i
				break
			}
		}
		if index < 0 || r.Quantity > c.Items[index].Quantity {
			return domain.ErrNotFound
		}
		item := c.Items[index]
		stored.Item.CashID, stored.Item.ExpiresAt = item.CashID, item.ExpiresAt
		left := item.Quantity - r.Quantity
		if item.CashID > 0 && left > 0 {
			// A split stack needs its own serial so each quantity can be moved
			// independently without duplicating a locker item.
			if err := tx.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,state) SELECT account_id,world_id,sn,template_id,$1,inv_type,expires_at,'storage' FROM asset_cash_items WHERE id=$2 AND character_id=$3 AND state='inventory' RETURNING id`, r.Quantity, item.CashID, c.ID).Scan(&stored.Item.CashID); err != nil {
				return err
			}
		}
		if left == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1`, item.ID); err != nil {
				return err
			}
			c.Items = append(c.Items[:index], c.Items[index+1:]...)
			result.Inventory.Changes = []domain.InventoryChange{{Mode: 3, InvType: item.InvType, Slot: r.Slot}}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE asset_items SET quantity=$1,revision=revision+1 WHERE id=$2`, left, item.ID); err != nil {
				return err
			}
			c.Items[index].Quantity = left
			c.Items[index].Revision++
			result.Inventory.Changes = []domain.InventoryChange{{Mode: 1, InvType: item.InvType, Slot: r.Slot, Quantity: left}}
		}
	}
	if err := guardItemExpiry(ctx, tx, stored.Item.ExpiresAt); err != nil {
		return err
	}
	if stored.Item.CashID > 0 {
		updated, err := tx.ExecContext(ctx, `UPDATE asset_cash_items SET state='storage',character_id=NULL,slot=0,quantity=$1,world_id=$4 WHERE id=$2 AND account_id=$3 AND state IN('inventory','storage')`, stored.Item.Quantity, stored.Item.CashID, c.AccountID, c.WorldID)
		if err != nil {
			return err
		}
		if n, _ := updated.RowsAffected(); n != 1 {
			return domain.ErrStaleOwner
		}
	}
	var equipment []byte
	if stored.Equipment != nil {
		equipment, _ = json.Marshal(stored.Equipment)
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO asset_storage_items(account_id,world_id,position,inv_type,template_id,quantity,equipment,cash_id,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, c.AccountID, c.WorldID, stored.Position, stored.Item.InvType, stored.Item.TemplateID, stored.Item.Quantity, nullableJSON(equipment), stored.Item.CashID, stored.Item.ExpiresAt).Scan(&stored.ID); err != nil {
		return err
	}
	result.Storage.Items = append(result.Storage.Items, stored)
	c.Mesos -= 100
	return nil
}

func withdrawStorage(ctx context.Context, tx *sql.Tx, r domain.StorageRequest, result *domain.StorageResult) error {
	index := -1
	for i, item := range result.Storage.Items {
		if item.ID == r.StorageItemID {
			index = i
			break
		}
	}
	if index < 0 {
		return domain.ErrNotFound
	}
	item := result.Storage.Items[index]
	if err := guardItemExpiry(ctx, tx, item.Item.ExpiresAt); err != nil {
		return err
	}
	asset := domain.TradeItem{InvType: item.Item.InvType, Quantity: item.Item.Quantity, Equipment: item.Equipment, Item: &item.Item}
	if err := receiveTradeItem(ctx, tx, &result.Inventory, asset); err != nil {
		return err
	}
	if item.Item.CashID > 0 {
		updated, err := tx.ExecContext(ctx, `UPDATE asset_cash_items SET state='inventory',character_id=$1,world_id=$4 WHERE id=$2 AND account_id=$3 AND state='storage'`, result.Inventory.Character.ID, item.Item.CashID, result.Inventory.Character.AccountID, result.Inventory.Character.WorldID)
		if err != nil {
			return err
		}
		if n, _ := updated.RowsAffected(); n != 1 {
			return domain.ErrStaleOwner
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM asset_storage_items WHERE id=$1`, item.ID); err != nil {
		return err
	}
	result.Storage.Items = append(result.Storage.Items[:index], result.Storage.Items[index+1:]...)
	return nil
}
