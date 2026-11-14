package persistence

import (
	"context"
	"database/sql"
	"errors"

	"maple-go-server/internal/domain"
)

// ResumeChannelTransfer is read-only. It succeeds only when the exact
// transition FinishChannelTransfer would have made is durable, so a resend
// after an ambiguous commit cannot fork or skip a generation.
func (s *Store) ResumeChannelTransfer(ctx context.Context, session domain.CharacterSession, target int) (domain.CharacterSession, error) {
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
	next := session.Generation + 1
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT s.generation FROM ops_account_sessions s JOIN ops_character_ownership o ON o.character_id=$2 AND o.account_id=s.account_id AND o.session_id=s.session_id AND o.generation=s.generation AND o.instance_id=s.owner_instance AND o.epoch=s.world_epoch
 WHERE s.account_id=$1 AND s.session_id=$3 AND s.generation=$4 AND s.state='world' AND s.owner_instance=$5 AND s.world_epoch=$6 AND o.channel_id=$7 FOR SHARE OF s`, session.AccountID, session.CharacterID, session.SessionID, next, session.Lease.InstanceID, session.Lease.Epoch, target).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	if err != nil {
		return domain.CharacterSession{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.CharacterSession{}, err
	}
	session.Generation = generation
	session.ChannelID = target
	return session, nil
}

// ListWorldSessions returns every in-world session this exact lease owns.
func (s *Store) ListWorldSessions(ctx context.Context, lease domain.WorldLease) ([]domain.OwnedSession, error) {
	return s.listOwnedSessions(ctx, `SELECT s.account_id,s.character_id,s.session_id,s.generation,COALESCE(o.channel_id,0) FROM ops_account_sessions s
 LEFT JOIN ops_character_ownership o ON o.character_id=s.character_id AND o.session_id=s.session_id AND o.generation=s.generation
 WHERE s.state='world' AND s.world_id=$1 AND s.owner_instance=$2 AND s.world_epoch=$3`, lease.WorldID, lease.InstanceID, lease.Epoch)
}

// ListCashSessions returns every cashshop session this exact lease owns.
func (s *Store) ListCashSessions(ctx context.Context, lease domain.ServiceLease) ([]domain.OwnedSession, error) {
	return s.listOwnedSessions(ctx, `SELECT account_id,character_id,session_id,generation,0 FROM ops_account_sessions WHERE state='cashshop' AND owner_instance=$1 AND world_epoch=$2`, lease.InstanceID, lease.Epoch)
}

func (s *Store) listOwnedSessions(ctx context.Context, query string, args ...any) ([]domain.OwnedSession, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var owned []domain.OwnedSession
	for rows.Next() {
		var o domain.OwnedSession
		if err = rows.Scan(&o.AccountID, &o.CharacterID, &o.SessionID, &o.Generation, &o.ChannelID); err != nil {
			return nil, err
		}
		owned = append(owned, o)
	}
	return owned, rows.Err()
}
