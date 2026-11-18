package persistence

import (
	"context"
	"database/sql"

	"maple-go-server/internal/domain"
)

var _ domain.EventDropRepository = (*Store)(nil)

func (s *Store) TransformEventDrop(ctx context.Context, drop domain.ItemDrop, templateID, x, y int) (domain.ItemDrop, bool, error) {
	result := domain.ItemDrop{
		WorldID: drop.WorldID, ChannelID: drop.ChannelID, MapID: drop.MapID, OwnerID: drop.OwnerID,
		InvType: templateID / 1000000, TemplateID: templateID, Quantity: 1, X: x, Y: y,
	}
	if result.InvType < 2 || result.InvType > 4 {
		return domain.ItemDrop{}, false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ItemDrop{}, false, err
	}
	defer tx.Rollback()
	ok, err := consumeEventDrop(ctx, tx, drop)
	if err != nil || !ok {
		return domain.ItemDrop{}, false, err
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO asset_item_drops (world_id,channel_id,map_id,owner_id,x,y,inv_type,template_id,quantity)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1) RETURNING object_id`,
		result.WorldID, result.ChannelID, result.MapID, result.OwnerID, x, y, result.InvType, templateID).Scan(&result.ObjectID)
	if err != nil {
		return domain.ItemDrop{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ItemDrop{}, false, err
	}
	return result, true, nil
}

func (s *Store) ConsumeEventDrop(ctx context.Context, drop domain.ItemDrop) (bool, error) {
	return consumeEventDrop(ctx, s.db, drop)
}

func consumeEventDrop(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, drop domain.ItemDrop) (bool, error) {
	result, err := db.ExecContext(ctx, `UPDATE asset_item_drops SET expired_at=clock_timestamp()
		WHERE object_id=$1 AND world_id=$2 AND channel_id=$3 AND map_id=$4 AND owner_id=$5
		  AND template_id=$6 AND quantity=$7 AND x=$8 AND y=$9 AND player_drop
		  AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`,
		drop.ObjectID, drop.WorldID, drop.ChannelID, drop.MapID, drop.OwnerID, drop.TemplateID, drop.Quantity, drop.X, drop.Y)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) ExpireEventDrops(ctx context.Context, world, channel, mapID int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE asset_item_drops SET expired_at=clock_timestamp()
		WHERE world_id=$1 AND channel_id=$2 AND map_id=$3
		  AND picked_up_by IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`, world, channel, mapID)
	return err
}
