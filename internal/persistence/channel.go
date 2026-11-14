package persistence

import (
	"context"
	"database/sql"
	"errors"

	"maple-go-server/internal/domain"
)

// BeginChannelTransfer freezes the writer while the same world process retains
// the full in-memory aggregate. The destination must consume it within 30s.
func (s *Store) BeginChannelTransfer(ctx context.Context, session domain.CharacterSession, target int) error {
	if target < 1 || target > 20 || target == session.ChannelID {
		return domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, session.Lease); err != nil {
		return err
	}
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='world' AND owner_instance=$4 AND world_epoch=$5 FOR UPDATE`, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE ops_character_ownership SET channel_id=$1 WHERE character_id=$2 AND account_id=$3 AND session_id=$4 AND generation=$5 AND instance_id=$6 AND epoch=$7 AND channel_id=$8`, target, session.CharacterID, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch, session.ChannelID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrStaleOwner
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ops_account_sessions SET state='transfer',expires_at=clock_timestamp()+interval '30 seconds' WHERE account_id=$1`, session.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FinishChannelTransfer(ctx context.Context, session domain.CharacterSession, target int) (domain.CharacterSession, error) {
	if target < 1 || target > 20 || target == session.ChannelID {
		return domain.CharacterSession{}, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CharacterSession{}, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, session.Lease); err != nil {
		return domain.CharacterSession{}, err
	}
	var generation int64
	err = tx.QueryRowContext(ctx, `UPDATE ops_account_sessions SET state='world',generation=generation+1,expires_at=$6 WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND owner_instance=$4 AND world_epoch=$5 AND state='transfer' AND expires_at>clock_timestamp() RETURNING generation`, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch, session.Lease.ExpiresAt).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	if err != nil {
		return domain.CharacterSession{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE ops_character_ownership SET generation=$1 WHERE character_id=$2 AND account_id=$3 AND session_id=$4 AND generation=$5 AND instance_id=$6 AND epoch=$7 AND channel_id=$8`, generation, session.CharacterID, session.AccountID, session.SessionID, session.Generation, session.Lease.InstanceID, session.Lease.Epoch, target)
	if err != nil {
		return domain.CharacterSession{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return domain.CharacterSession{}, err
	}
	if n != 1 {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	if err = tx.Commit(); err != nil {
		return domain.CharacterSession{}, err
	}
	session.Generation = generation
	session.ChannelID = target
	return session, nil
}
