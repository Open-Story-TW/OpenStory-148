package persistence

import (
	"context"
	"database/sql"
	"maple-go-server/internal/domain"
)

func readInventorySlots(ctx context.Context, q txQuery, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT inv_type,capacity FROM asset_inventory_capacity WHERE character_id=$1 ORDER BY inv_type`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var invType, capacity int
		if err = rows.Scan(&invType, &capacity); err != nil {
			return err
		}
		if invType < 1 || invType > 5 || capacity < domain.DefaultInventorySlots || capacity > domain.MaxInventorySlots {
			return domain.ErrInvalidInput
		}
		c.InventorySlots[invType-1] = capacity
	}
	return rows.Err()
}

// ExpandInventorySlotsSQL is a transaction composition helper: the caller must
// fence/lock the character session first and commit its reward receipt in tx.
// The character row lock also serializes first insertion for a previously absent bag.
func ExpandInventorySlotsSQL(ctx context.Context, tx *sql.Tx, c *domain.Character, invType, delta int) error {
	if c == nil || c.ID <= 0 || c.AccountID <= 0 || invType < 1 || invType > 5 || delta <= 0 || delta > domain.MaxInventorySlots {
		return domain.ErrInvalidInput
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 AND account_id=$2 FOR UPDATE`, c.ID, c.AccountID).Scan(&id); err != nil {
		return err
	}
	var capacity int
	err := tx.QueryRowContext(ctx, `INSERT INTO asset_inventory_capacity(character_id,inv_type,capacity) VALUES($1,$2,LEAST(96,24+$3)) ON CONFLICT(character_id,inv_type) DO UPDATE SET capacity=LEAST(96,asset_inventory_capacity.capacity+$3) RETURNING capacity`, c.ID, invType, delta).Scan(&capacity)
	if err != nil {
		return err
	}
	c.InventorySlots[invType-1] = capacity
	return nil
}

func FreeEquipmentSlot(c *domain.Character) int {
	if c == nil {
		return 0
	}
	for slot := 1; slot <= c.Capacity(1); slot++ {
		free := true
		for _, item := range c.Equipment {
			if item.Slot == slot {
				free = false
				break
			}
		}
		if free {
			return slot
		}
	}
	return 0
}
