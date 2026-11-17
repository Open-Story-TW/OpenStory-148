package persistence

import (
	"context"
	"database/sql"

	"maple-go-server/internal/domain"
)

var _ domain.EventDropRepository = (*Store)(nil)

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
