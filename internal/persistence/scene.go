package persistence

import (
	"context"
	"database/sql"
	"errors"

	"maple-go-server/internal/domain"
)

var _ domain.SceneRepository = (*Store)(nil)

func (s *Store) ValidateSceneOwner(ctx context.Context, v domain.CharacterSession) error {
	if v.AccountID <= 0 || v.CharacterID <= 0 || v.Character.ID != v.CharacterID || v.ChannelID <= 0 || v.SessionID == "" || v.Generation <= 0 {
		return domain.ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, v.Lease); err != nil {
		return err
	}
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='world' AND world_id=$4 AND character_id=$5 AND owner_instance=$6 AND world_epoch=$7 FOR SHARE`, v.AccountID, v.SessionID, v.Generation, v.Lease.WorldID, v.CharacterID, v.Lease.InstanceID, v.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_character_ownership WHERE account_id=$1 AND character_id=$2 AND session_id=$3 AND generation=$4 AND world_id=$5 AND instance_id=$6 AND epoch=$7 AND channel_id=$8 FOR SHARE`, v.AccountID, v.CharacterID, v.SessionID, v.Generation, v.Lease.WorldID, v.Lease.InstanceID, v.Lease.Epoch, v.ChannelID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	return err
}
