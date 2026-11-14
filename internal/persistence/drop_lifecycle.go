package persistence

import (
	"context"

	"maple-go-server/internal/domain"
)

var _ domain.DropLifecycleRepository = (*Store)(nil)

// ClearMapDrops expires every item and meso drop lying on one map now (the
// GM !cleardrops). The drops are gone for good, like any expired drop.
func (s *Store) ClearMapDrops(ctx context.Context, lease domain.WorldLease, channelID, mapID int) ([]domain.ItemDrop, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, lease); err != nil {
		return nil, err
	}
	// As in ExpireItemDrops, a drop being picked up right now is left alone.
	rows, err := tx.QueryContext(ctx, `WITH due AS (
	 SELECT object_id FROM asset_item_drops
	 WHERE world_id=$1 AND channel_id=$2 AND map_id=$3 AND picked_up_by IS NULL AND expired_at IS NULL
	 ORDER BY object_id LIMIT 2000 FOR UPDATE SKIP LOCKED
	) UPDATE asset_item_drops d SET expired_at=clock_timestamp()
	FROM due WHERE d.object_id=due.object_id
	RETURNING d.object_id,d.world_id,d.channel_id,d.map_id,d.quest_id`, lease.WorldID, channelID, mapID)
	if err != nil {
		return nil, err
	}
	var cleared []domain.ItemDrop
	for rows.Next() {
		var drop domain.ItemDrop
		if err = rows.Scan(&drop.ObjectID, &drop.WorldID, &drop.ChannelID, &drop.MapID, &drop.QuestID); err != nil {
			rows.Close()
			return nil, err
		}
		cleared = append(cleared, drop)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return cleared, nil
}

func (s *Store) ExpireItemDrops(ctx context.Context, lease domain.WorldLease, limit int) ([]domain.ItemDrop, error) {
	if limit < 1 || limit > 512 {
		return nil, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, lease); err != nil {
		return nil, err
	}
	// The same row lock arbitrates expiry and PickupItem. SKIP LOCKED keeps a
	// slow pickup from delaying unrelated drops; the next cycle retries it.
	rows, err := tx.QueryContext(ctx, `WITH due AS (
	 SELECT object_id FROM asset_item_drops
	 WHERE world_id=$1 AND picked_up_by IS NULL AND expired_at IS NULL
	 AND expires_at<=clock_timestamp()
	 ORDER BY expires_at,object_id LIMIT $2 FOR UPDATE SKIP LOCKED
	) UPDATE asset_item_drops d SET expired_at=clock_timestamp()
	FROM due WHERE d.object_id=due.object_id
	RETURNING d.object_id,d.world_id,d.channel_id,d.map_id,d.quest_id`, lease.WorldID, limit)
	if err != nil {
		return nil, err
	}
	var expired []domain.ItemDrop
	for rows.Next() {
		var drop domain.ItemDrop
		if err = rows.Scan(&drop.ObjectID, &drop.WorldID, &drop.ChannelID, &drop.MapID, &drop.QuestID); err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, drop)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return expired, nil
}
