package persistence

import (
	"context"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

// ConsumeZakumEye removes exactly one settled ground offering. Moving the
// item out of inventory first uses the standard native drop transaction; a
// failed offering leaves a real item the owner can pick up again.
func (s *Store) ConsumeZakumEye(ctx context.Context, session domain.CharacterSession, objectID int32) error {
	altar, ok := content.ZakumAltarByMap(session.Character.MapID)
	if !ok || objectID <= 0 || session.ChannelID < 1 {
		return domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := guardBuddyOwner(ctx, tx, session); err != nil {
		return err
	}
	world, channel, mapID := session.Lease.WorldID, session.ChannelID, session.Character.MapID
	if err := lockItemRoom(ctx, tx, world, channel, mapID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE asset_item_drops SET expired_at=clock_timestamp()
		WHERE object_id=$1 AND world_id=$2 AND channel_id=$3 AND map_id=$4 AND owner_id=$5
		  AND template_id=$6 AND quantity=1 AND picked_up_by IS NULL AND expired_at IS NULL
		  AND expires_at>clock_timestamp() AND source_operation_id IS NULL
		  AND x BETWEEN $7 AND $8 AND y BETWEEN $9 AND $10`,
		objectID, world, channel, mapID, session.CharacterID, content.ZakumEye,
		altar.X-183, altar.X+201, altar.Y-30, altar.Y)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}
