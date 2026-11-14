package persistence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"maple-go-server/internal/domain"
	"math"
	"net"
	"time"
)

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Store) BeginLogin(ctx context.Context, accountID int64, sessionID string) (domain.LoginSession, error) {
	if sessionID == "" || len(sessionID) > 128 {
		return domain.LoginSession{}, domain.ErrInvalidInput
	}
	var generation int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO ops_account_sessions(account_id,session_id,state,expires_at) VALUES($1,$2,'login',clock_timestamp()+interval '6 minutes')
 ON CONFLICT(account_id) DO UPDATE SET session_id=EXCLUDED.session_id,state='login',generation=ops_account_sessions.generation+1,
 world_id=-1,character_id=0,owner_instance='',world_epoch=0,expires_at=clock_timestamp()+interval '6 minutes'
 WHERE ops_account_sessions.state='offline' OR (ops_account_sessions.state NOT IN('world','cashshop') AND ops_account_sessions.expires_at<=clock_timestamp())
 OR (ops_account_sessions.state='world' AND NOT EXISTS(SELECT 1 FROM ops_runtime_leases l WHERE l.world_id=ops_account_sessions.world_id AND l.instance_id=ops_account_sessions.owner_instance AND l.epoch=ops_account_sessions.world_epoch AND l.expires_at>clock_timestamp()))
 OR (ops_account_sessions.state='cashshop' AND NOT EXISTS(SELECT 1 FROM ops_service_leases l WHERE l.service='cashshop' AND l.instance_id=ops_account_sessions.owner_instance AND l.epoch=ops_account_sessions.world_epoch AND l.expires_at>clock_timestamp()))
 RETURNING generation`, accountID, sessionID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LoginSession{}, domain.ErrAlreadyOnline
	}
	if err != nil {
		return domain.LoginSession{}, err
	}
	return domain.LoginSession{AccountID: accountID, SessionID: sessionID, Generation: generation}, nil
}
func (s *Store) EndLogin(ctx context.Context, session domain.LoginSession) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET state='offline',expires_at=clock_timestamp() WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='login'`, session.AccountID, session.SessionID, session.Generation)
	return err
}
func (s *Store) TouchLogin(ctx context.Context, session domain.LoginSession) error {
	result, err := s.db.ExecContext(ctx, `UPDATE ops_account_sessions SET expires_at=clock_timestamp()+interval '6 minutes' WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='login' AND expires_at>clock_timestamp()`, session.AccountID, session.SessionID, session.Generation)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return domain.ErrStaleOwner
	}
	return nil
}
func (s *Store) AcquireWorldLease(ctx context.Context, worldID int, instanceID string) (domain.WorldLease, error) {
	var lease domain.WorldLease
	lease.WorldID = worldID
	lease.InstanceID = instanceID
	if worldID < 0 || worldID > 127 || instanceID == "" {
		return lease, domain.ErrInvalidInput
	}
	err := s.db.QueryRowContext(ctx, `INSERT INTO ops_runtime_leases(world_id,instance_id,epoch,expires_at) VALUES($1,$2,1,clock_timestamp()+interval '20 seconds')
 ON CONFLICT(world_id) DO UPDATE SET instance_id=EXCLUDED.instance_id,
 epoch=CASE WHEN ops_runtime_leases.instance_id=EXCLUDED.instance_id AND ops_runtime_leases.expires_at>clock_timestamp() THEN ops_runtime_leases.epoch ELSE ops_runtime_leases.epoch+1 END,
 expires_at=clock_timestamp()+interval '20 seconds'
 WHERE ops_runtime_leases.expires_at<=clock_timestamp() OR ops_runtime_leases.instance_id=EXCLUDED.instance_id RETURNING epoch,expires_at`, worldID, instanceID).Scan(&lease.Epoch, &lease.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return lease, domain.ErrLeaseBusy
	}
	return lease, err
}
func (s *Store) RenewWorldLease(ctx context.Context, lease domain.WorldLease) (domain.WorldLease, error) {
	err := s.db.QueryRowContext(ctx, `UPDATE ops_runtime_leases SET expires_at=clock_timestamp()+interval '20 seconds' WHERE world_id=$1 AND instance_id=$2 AND epoch=$3 AND expires_at>clock_timestamp() RETURNING expires_at`, lease.WorldID, lease.InstanceID, lease.Epoch).Scan(&lease.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return lease, domain.ErrStaleOwner
	}
	return lease, err
}
func (s *Store) ReleaseWorldLease(ctx context.Context, lease domain.WorldLease) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ops_runtime_leases SET expires_at=clock_timestamp() WHERE world_id=$1 AND instance_id=$2 AND epoch=$3`, lease.WorldID, lease.InstanceID, lease.Epoch)
	return err
}

type txQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanOne(ctx context.Context, tx txQuery, query string, args []any, dest ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return rows.Err()
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

func guardLogin(ctx context.Context, tx txQuery, session domain.LoginSession) error {
	if session.AccountID <= 0 || session.SessionID == "" || session.Generation < 1 {
		return domain.ErrStaleOwner
	}
	var generation int64
	err := scanOne(ctx, tx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='login' AND expires_at>clock_timestamp() FOR SHARE`, []any{session.AccountID, session.SessionID, session.Generation}, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	return err
}

func guardLease(ctx context.Context, tx txQuery, lease domain.WorldLease) error {
	rows, err := tx.QueryContext(ctx, `SELECT epoch FROM ops_runtime_leases WHERE world_id=$1 AND instance_id=$2 AND epoch=$3 AND expires_at>clock_timestamp() FOR SHARE`, lease.WorldID, lease.InstanceID, lease.Epoch)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return rows.Err()
		}
		return domain.ErrStaleOwner
	}
	return nil
}
func (s *Store) CreateTransfer(ctx context.Context, r domain.TransferRequest) (domain.Transfer, error) {
	if r.ChannelID < 1 || r.ChannelID > 20 || r.AccountID <= 0 || r.CharacterID <= 0 || r.SessionID == "" || net.ParseIP(r.ClientIP) == nil {
		return domain.Transfer{}, domain.ErrInvalidInput
	}
	id, err := randomToken(16)
	if err != nil {
		return domain.Transfer{}, err
	}
	token, err := randomToken(32)
	if err != nil {
		return domain.Transfer{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Transfer{}, err
	}
	defer tx.Rollback()
	var instance string
	var epoch int64
	if err = tx.QueryRowContext(ctx, `SELECT instance_id,epoch FROM ops_runtime_leases WHERE world_id=$1 AND expires_at>clock_timestamp() FOR SHARE`, r.WorldID).Scan(&instance, &epoch); err != nil {
		return domain.Transfer{}, domain.ErrStaleOwner
	}
	var sessionID, state string
	if err = tx.QueryRowContext(ctx, `SELECT session_id,state FROM ops_account_sessions WHERE account_id=$1 AND expires_at>clock_timestamp() FOR UPDATE`, r.AccountID).Scan(&sessionID, &state); err != nil || sessionID != r.SessionID || state != "login" {
		return domain.Transfer{}, domain.ErrForbidden
	}
	var idCheck int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 AND account_id=$2 AND world_id=$3`, r.CharacterID, r.AccountID, r.WorldID).Scan(&idCheck); err != nil {
		return domain.Transfer{}, domain.ErrForbidden
	}
	// A ban set while the account sat at character select stops it here.
	if banned, e := banQuery(ctx, tx, r.AccountID, r.ClientIP); e != nil || banned {
		return domain.Transfer{}, domain.ErrForbidden
	}
	// The v145 client stores this value as its own character ID
	// (CWvsContext+0x20C0) and echoes it on migrate-in; party leader checks
	// compare against it. It must therefore be the character ID, never a
	// random handle. Uniqueness is per pending ticket: an older unconsumed
	// ticket for the same character is superseded by this login.
	if r.CharacterID < 1 || r.CharacterID > math.MaxInt32 {
		return domain.Transfer{}, domain.ErrInvalidInput
	}
	handle := r.CharacterID
	if _, err = tx.ExecContext(ctx, `UPDATE ops_transfers SET consumed_at=clock_timestamp() WHERE client_handle=$1 AND consumed_at IS NULL`, handle); err != nil {
		return domain.Transfer{}, err
	}
	var expiry time.Time
	if err = tx.QueryRowContext(ctx, `INSERT INTO ops_transfers(id,token_hash,client_handle,account_id,character_id,world_id,channel_id,session_id,client_ip,target_instance,target_epoch,expires_at)
  VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,clock_timestamp()+interval '30 seconds') RETURNING expires_at`, id, tokenHash(token), handle, r.AccountID, r.CharacterID, r.WorldID, r.ChannelID, r.SessionID, r.ClientIP, instance, epoch).Scan(&expiry); err != nil {
		return domain.Transfer{}, err
	}
	if expiry.IsZero() {
		return domain.Transfer{}, domain.ErrTransferInvalid
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ops_account_sessions SET state='transfer',world_id=$2,character_id=$3,expires_at=$4 WHERE account_id=$1`, r.AccountID, r.WorldID, r.CharacterID, expiry); err != nil {
		return domain.Transfer{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Transfer{}, err
	}
	return domain.Transfer{ID: id, Token: token, ClientHandle: handle, CharacterID: r.CharacterID, ExpiresAt: expiry}, nil
}
func (s *Store) ConsumeTransfer(ctx context.Context, c domain.TransferClaim) (domain.CharacterSession, error) {
	if c.WorldID != c.Lease.WorldID || c.ChannelID < 1 || c.ChannelID > 20 {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	legacy := c.Token == ""
	if legacy && (!c.AllowLegacyLoopback || c.ClientHandle <= 0 || !net.ParseIP(c.ClientIP).IsLoopback()) {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	var id, hash, sourceIP, sessionID, target string
	var accountID, characterID, epoch int64
	var expiry time.Time
	err := s.db.QueryRowContext(ctx, `SELECT id,token_hash,client_ip,session_id,account_id,character_id,target_instance,target_epoch,expires_at FROM ops_transfers
 WHERE world_id=$1 AND channel_id=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp() AND (($3>0 AND client_handle=$3) OR ($4<>'' AND token_hash=$4))`, c.WorldID, c.ChannelID, c.ClientHandle, func() string {
		if c.Token == "" {
			return ""
		}
		return tokenHash(c.Token)
	}()).Scan(&id, &hash, &sourceIP, &sessionID, &accountID, &characterID, &target, &epoch, &expiry)
	if err != nil {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	if c.Token != "" && tokenHash(c.Token) != hash {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	if c.CharacterID > 0 && c.CharacterID != characterID {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	if sourceIP != c.ClientIP || target != c.Lease.InstanceID || epoch != c.Lease.Epoch {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	if legacy && !net.ParseIP(sourceIP).IsLoopback() {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return domain.CharacterSession{}, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, c.Lease); err != nil {
		return domain.CharacterSession{}, err
	}
	var currentSession, state string
	var generation int64
	if err = scanOne(ctx, tx, `SELECT session_id,state,generation FROM ops_account_sessions WHERE account_id=$1 FOR UPDATE`, []any{accountID}, &currentSession, &state, &generation); err != nil || currentSession != sessionID || state != "transfer" {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	result, err := tx.ExecContext(ctx, `UPDATE ops_transfers SET consumed_at=clock_timestamp() WHERE id=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, id)
	if err != nil {
		return domain.CharacterSession{}, err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	generation++
	if _, err = tx.ExecContext(ctx, `UPDATE ops_account_sessions SET state='world',generation=$2,owner_instance=$3,world_epoch=$4,expires_at=$5 WHERE account_id=$1`, accountID, generation, c.Lease.InstanceID, c.Lease.Epoch, c.Lease.ExpiresAt); err != nil {
		return domain.CharacterSession{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_character_ownership(character_id,account_id,world_id,instance_id,epoch,session_id,generation,channel_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
 ON CONFLICT(character_id) DO UPDATE SET account_id=EXCLUDED.account_id,world_id=EXCLUDED.world_id,instance_id=EXCLUDED.instance_id,epoch=EXCLUDED.epoch,session_id=EXCLUDED.session_id,generation=EXCLUDED.generation,channel_id=EXCLUDED.channel_id`, characterID, accountID, c.WorldID, c.Lease.InstanceID, c.Lease.Epoch, sessionID, generation, c.ChannelID); err != nil {
		return domain.CharacterSession{}, err
	}
	// Ownership and transfer consumption must roll back if any character
	// component fails to load; the handler has no session to release on failure.
	value, err := readCharacter(ctx, tx.Client(), characterID)
	if err != nil {
		return domain.CharacterSession{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.CharacterSession{}, err
	}
	return domain.CharacterSession{AccountID: accountID, CharacterID: characterID, SessionID: sessionID, Generation: generation, ChannelID: c.ChannelID, Lease: c.Lease, Character: value}, nil
}
