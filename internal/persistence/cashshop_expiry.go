package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"maple-go-server/internal/domain"
)

var _ domain.CashLockerExpiryRepository = (*Store)(nil)

var errCashGender = errors.Join(domain.ErrCashGender, domain.ErrForbidden)

// cashExpiry follows Legacy CashShop.toItem: the safety charm (5320000) carries
// no WZ period yet is always sold as a 90-day item.
func cashExpiry(p domain.CashProduct) int64 {
	days := p.PeriodDays
	if p.TemplateID == 5320000 && days <= 0 {
		days = 90
	}
	if days <= 0 {
		return 0
	}
	return time.Now().Add(time.Duration(days) * 24 * time.Hour).UnixMilli()
}

// ExpireCashLocker retires expired locker records on the database clock, like
// Legacy CashShop.checkExpire. Each record is returned exactly once (the state
// change is the claim), so the caller can show the native notice once. Pets
// keep their own lifetime and are excluded as in Java.
func (s *Store) ExpireCashLocker(ctx context.Context, session domain.CashSession) ([]domain.CashItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, session); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `UPDATE asset_cash_items SET state='expired' WHERE account_id=$1 AND state='locker' AND expires_at>0 AND expires_at<=floor(extract(epoch FROM clock_timestamp())*1000) AND template_id/10000<>500 RETURNING id,account_id,character_id,sn,template_id,quantity,inv_type,slot,expires_at,state,equipment,gift_from`, session.Session.AccountID)
	if err != nil {
		return nil, err
	}
	expired := make([]domain.CashItem, 0)
	for rows.Next() {
		item, e := scanCashItem(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		expired = append(expired, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return expired, tx.Commit()
}

// ExpireCashInventory also retires timed ordinary bundles. It uses the same
// database clock, fencing and revision contract as item commands.
func (s *Store) ExpireCashInventory(ctx context.Context, session domain.CharacterSession) (domain.InventoryResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	defer tx.Rollback()
	c, err := lockInventoryCharacter(ctx, tx, session)
	if err != nil {
		return domain.InventoryResult{}, err
	}
	if c.Revision != session.Character.Revision {
		return domain.InventoryResult{}, domain.ErrStaleOwner
	}
	var now int64
	if err = tx.QueryRowContext(ctx, `SELECT floor(extract(epoch FROM clock_timestamp())*1000)::bigint`).Scan(&now); err != nil {
		return domain.InventoryResult{}, err
	}
	result := domain.InventoryResult{Character: c}
	oldHP, oldMP := equipmentVitals(c.Equipment)
	equipment := c.Equipment[:0]
	for _, item := range c.Equipment {
		if item.CashID > 0 && item.ExpiresAt > 0 && item.ExpiresAt <= now {
			if _, err = tx.ExecContext(ctx, `DELETE FROM asset_equipment WHERE id=$1 AND character_id=$2`, item.ID, c.ID); err != nil {
				return domain.InventoryResult{}, err
			}
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: 1, Slot: item.Slot})
		} else {
			equipment = append(equipment, item)
		}
	}
	c.Equipment = equipment
	items := c.Items[:0]
	for _, item := range c.Items {
		// An expired pet stays in the bag (Legacy keeps the "doll"); its summon
		// is recalled by the pet tick and refused while expired.
		if item.ExpiresAt > 0 && item.ExpiresAt <= now && !domain.IsPetTemplate(item.TemplateID) {
			if _, err = tx.ExecContext(ctx, `DELETE FROM asset_items WHERE id=$1 AND character_id=$2`, item.ID, c.ID); err != nil {
				return domain.InventoryResult{}, err
			}
			result.Changes = append(result.Changes, domain.InventoryChange{Mode: 3, InvType: item.InvType, Slot: item.Slot})
		} else {
			items = append(items, item)
		}
	}
	c.Items = items
	if len(result.Changes) == 0 {
		return result, nil
	}
	if err = recomputeEquipmentVitals(&c, oldHP, oldMP); err != nil {
		return domain.InventoryResult{}, err
	}
	c.HP, c.MP = min(session.Character.HP, c.MaxHP), min(session.Character.MP, c.MaxMP)
	if c.HP < 0 || c.MP < 0 {
		return domain.InventoryResult{}, domain.ErrInvalidInput
	}
	if err = commitInventoryState(ctx, tx, &c); err != nil {
		return domain.InventoryResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.InventoryResult{}, err
	}
	s.roster.Invalidate(ctx, c.AccountID, c.WorldID)
	result.Character = c
	return result, nil
}

func expireStoredCash(ctx context.Context, tx *sql.Tx, accountID int64, worldID int) error {
	// Use physical storage ownership as authority, including legacy records
	// whose cash metadata still names the world where they were purchased.
	var expired int64
	err := tx.QueryRowContext(ctx, `WITH expired AS (
 DELETE FROM asset_storage_items WHERE account_id=$1 AND world_id=$2 AND cash_id>0 AND expires_at>0 AND expires_at<=floor(extract(epoch FROM clock_timestamp())*1000) RETURNING cash_id
), marked AS (
 UPDATE asset_cash_items SET state='expired',slot=0,world_id=$2 WHERE account_id=$1 AND id IN(SELECT cash_id FROM expired) RETURNING id
) SELECT count(*) FROM expired`, accountID, worldID).Scan(&expired)
	if err != nil {
		return err
	}
	if expired > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE asset_storages SET revision=revision+1 WHERE account_id=$1 AND world_id=$2`, accountID, worldID); err != nil {
			return err
		}
	}
	return nil
}
