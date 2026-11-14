package persistence

import (
	"context"
	"database/sql"

	"maple-go-server/internal/domain"
)

func guardItemExpiry(ctx context.Context, tx *sql.Tx, expiry int64) error {
	if expiry == 0 {
		return nil
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT $1 > extract(epoch FROM clock_timestamp())*1000`, expiry).Scan(&active); err != nil {
		return err
	}
	if !active {
		return domain.ErrForbidden
	}
	return nil
}

// Cash bundles keep independent serials and expiry, so they may not merge into
// an ordinary stack, even if the template matches.
func receiveCashBundle(ctx context.Context, tx *sql.Tx, result *domain.InventoryResult, item domain.InventoryItem) error {
	slot := 0
	for n := 1; n <= result.Character.Capacity(item.InvType); n++ {
		free := true
		for _, existing := range result.Character.Items {
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
	if slot == 0 {
		return domain.ErrInsufficient
	}
	item.Slot, item.Revision = slot, 1
	if err := tx.QueryRowContext(ctx, `INSERT INTO asset_items(character_id,inv_type,slot,template_id,quantity) VALUES($1,$2,$3,$4,$5) RETURNING id`, result.Character.ID, item.InvType, slot, item.TemplateID, item.Quantity).Scan(&item.ID); err != nil {
		return err
	}
	result.Character.Items = append(result.Character.Items, item)
	result.Changes = append(result.Changes, domain.InventoryChange{Mode: 0, InvType: item.InvType, Slot: slot, Item: &item})
	return nil
}
