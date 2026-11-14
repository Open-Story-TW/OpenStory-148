package persistence

import (
	"context"
	"database/sql"
	"encoding/json"

	"maple-go-server/internal/domain"
)

func applyCashMetadata(ctx context.Context, q txQuery, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT id,inv_type,slot,template_id,expires_at FROM asset_cash_items WHERE character_id=$1 AND state='inventory'`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, expiry int64
		var invType, slot, template int
		if err = rows.Scan(&id, &invType, &slot, &template, &expiry); err != nil {
			return err
		}
		if invType == 1 {
			for i := range c.Equipment {
				if c.Equipment[i].Slot == slot && c.Equipment[i].ItemID == template {
					c.Equipment[i].CashID = id
					c.Equipment[i].ExpiresAt = expiry
				}
			}
		} else {
			for i := range c.Items {
				if c.Items[i].InvType == invType && c.Items[i].Slot == slot && c.Items[i].TemplateID == template {
					c.Items[i].CashID = id
					c.Items[i].ExpiresAt = expiry
				}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = applyEquipmentMetadata(ctx, q, c); err != nil {
		return err
	}
	// Pet and guild identity ride the same aggregate load as CASH serials.
	if err = readPets(ctx, q, c); err != nil {
		return err
	}
	return readGuildTag(ctx, q, c)
}

func syncCashLocations(ctx context.Context, tx *sql.Tx, c domain.Character) error {
	present := make(map[int64]bool)
	for _, item := range c.Equipment {
		if item.CashID > 0 {
			present[item.CashID] = true
			raw, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE asset_cash_items SET slot=$1,quantity=1,equipment=$4,world_id=$6 WHERE id=$2 AND character_id=$3 AND template_id=$5 AND state='inventory' AND account_id=$7`, item.Slot, item.CashID, c.ID, raw, item.ItemID, c.WorldID, c.AccountID); err != nil {
				return err
			}
		}
	}
	for _, item := range c.Items {
		if item.CashID > 0 {
			present[item.CashID] = true
			if _, err := tx.ExecContext(ctx, `UPDATE asset_cash_items SET slot=$1,quantity=$4,world_id=$6 WHERE id=$2 AND character_id=$3 AND template_id=$5 AND state='inventory' AND account_id=$7`, item.Slot, item.CashID, c.ID, item.Quantity, item.TemplateID, c.WorldID, c.AccountID); err != nil {
				return err
			}
		}
	}
	// Fully consumed stacks must not later attach their serial to a reused slot,
	// nor reappear in the locker with their original purchased quantity.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM asset_cash_items WHERE character_id=$1 AND state='inventory' FOR UPDATE`, c.ID)
	if err != nil {
		return err
	}
	var consumed []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !present[id] {
			consumed = append(consumed, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range consumed {
		if _, err = tx.ExecContext(ctx, `UPDATE asset_cash_items SET state='expired',slot=0,world_id=$2 WHERE id=$1 AND account_id=$3`, id, c.WorldID, c.AccountID); err != nil {
			return err
		}
	}
	return nil
}
