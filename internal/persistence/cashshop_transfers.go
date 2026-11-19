package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"maple-go-server/internal/domain"
)

func (s *Store) AcquireCashLease(ctx context.Context, instance string) (domain.ServiceLease, error) {
	l := domain.ServiceLease{Service: "cashshop", InstanceID: instance}
	if instance == "" {
		return l, domain.ErrInvalidInput
	}
	err := s.db.QueryRowContext(ctx, `INSERT INTO ops_service_leases(service,instance_id,epoch,expires_at) VALUES('cashshop',$1,1,clock_timestamp()+interval '20 seconds') ON CONFLICT(service) DO UPDATE SET instance_id=EXCLUDED.instance_id,epoch=CASE WHEN ops_service_leases.instance_id=EXCLUDED.instance_id AND ops_service_leases.expires_at>clock_timestamp() THEN ops_service_leases.epoch ELSE ops_service_leases.epoch+1 END,expires_at=clock_timestamp()+interval '20 seconds' WHERE ops_service_leases.expires_at<=clock_timestamp() OR ops_service_leases.instance_id=EXCLUDED.instance_id RETURNING epoch,expires_at`, instance).Scan(&l.Epoch, &l.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrLeaseBusy
	}
	return l, err
}
func (s *Store) RenewCashLease(ctx context.Context, l domain.ServiceLease) (domain.ServiceLease, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return l, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `UPDATE ops_service_leases SET expires_at=clock_timestamp()+interval '20 seconds' WHERE service='cashshop' AND instance_id=$1 AND epoch=$2 AND expires_at>clock_timestamp() RETURNING expires_at`, l.InstanceID, l.Epoch).Scan(&l.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return l, domain.ErrStaleOwner
	}
	if err != nil {
		return l, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ops_account_sessions SET expires_at=$1 WHERE state='cashshop' AND owner_instance=$2 AND world_epoch=$3`, l.ExpiresAt, l.InstanceID, l.Epoch); err != nil {
		return l, err
	}
	return l, tx.Commit()
}
func (s *Store) ReleaseCashLease(ctx context.Context, l domain.ServiceLease) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ops_service_leases SET expires_at=clock_timestamp() WHERE service='cashshop' AND instance_id=$1 AND epoch=$2`, l.InstanceID, l.Epoch)
	return err
}
func guardCashLease(ctx context.Context, tx *sql.Tx, l domain.ServiceLease) error {
	var epoch int64
	err := tx.QueryRowContext(ctx, `SELECT epoch FROM ops_service_leases WHERE service='cashshop' AND instance_id=$1 AND epoch=$2 AND expires_at>clock_timestamp() FOR SHARE`, l.InstanceID, l.Epoch).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	return err
}
func guardCashSession(ctx context.Context, tx *sql.Tx, c domain.CashSession) error {
	if err := guardCashLease(ctx, tx, c.Lease); err != nil {
		return err
	}
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND character_id=$2 AND session_id=$3 AND generation=$4 AND state='cashshop' AND owner_instance=$5 AND world_epoch=$6 AND world_id=$7 FOR SHARE`, c.Session.AccountID, c.Session.CharacterID, c.Session.SessionID, c.Session.Generation, c.Lease.InstanceID, c.Lease.Epoch, c.Session.Lease.WorldID).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_character_ownership WHERE character_id=$1 AND account_id=$2 AND session_id=$3 AND generation=$4 AND instance_id=$5 AND epoch=$6 AND world_id=$7 AND channel_id=$8 FOR SHARE`, c.Session.CharacterID, c.Session.AccountID, c.Session.SessionID, c.Session.Generation, c.Lease.InstanceID, c.Lease.Epoch, c.Session.Lease.WorldID, c.ReturnChannel).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrStaleOwner
	}
	return err
}

func (s *Store) BeginCashTransfer(ctx context.Context, session domain.CharacterSession, handle int64, ip string) (domain.CashTransfer, error) {
	return s.beginCashStage(ctx, session, handle, ip, "cashshop")
}

// BeginITCTransfer is BeginCashTransfer for the 拍賣 button: the cash shop
// process opens the item trading center for this ticket.
func (s *Store) BeginITCTransfer(ctx context.Context, session domain.CharacterSession, handle int64, ip string) (domain.CashTransfer, error) {
	return s.beginCashStage(ctx, session, handle, ip, "itc")
}

func (s *Store) beginCashStage(ctx context.Context, session domain.CharacterSession, handle int64, ip, stage string) (domain.CashTransfer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashTransfer{}, err
	}
	defer tx.Rollback()
	value, err := lockInventoryCharacter(ctx, tx, session)
	if err != nil {
		return domain.CashTransfer{}, err
	}
	if value.Revision != session.Character.Revision {
		return domain.CashTransfer{}, domain.ErrStaleOwner
	}
	var l domain.ServiceLease
	l.Service = "cashshop"
	if err = tx.QueryRowContext(ctx, `SELECT instance_id,epoch,expires_at FROM ops_service_leases WHERE service='cashshop' AND expires_at>clock_timestamp() FOR SHARE`).Scan(&l.InstanceID, &l.Epoch, &l.ExpiresAt); err != nil {
		return domain.CashTransfer{}, domain.ErrStaleOwner
	}
	transfer, err := createServiceTicket(ctx, tx, session, handle, ip, "cashshop", session.ChannelID, l.InstanceID, l.Epoch)
	if err != nil {
		return transfer, err
	}
	if stage != "cashshop" {
		if _, err = tx.ExecContext(ctx, `UPDATE ops_service_transfers SET stage=$1 WHERE id=$2`, stage, transfer.ID); err != nil {
			return transfer, err
		}
	}
	return transfer, tx.Commit()
}

func createServiceTicket(ctx context.Context, tx *sql.Tx, session domain.CharacterSession, handle int64, ip, target string, channel int, instance string, epoch int64) (domain.CashTransfer, error) {
	if handle < 1 || handle > 2147483647 || !domain.TrustedClientIP(ip) || channel < 1 || channel > 20 {
		return domain.CashTransfer{}, domain.ErrInvalidInput
	}
	id, err := randomToken(16)
	if err != nil {
		return domain.CashTransfer{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(145013,$1)`, int32(handle)); err != nil {
		return domain.CashTransfer{}, err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ops_service_transfers WHERE native_handle=$1 AND target_service=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp())`, handle, target).Scan(&pending); err != nil {
		return domain.CashTransfer{}, err
	}
	if pending {
		return domain.CashTransfer{}, domain.ErrTransferInvalid
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ops_service_transfers SET consumed_at=clock_timestamp() WHERE native_handle=$1 AND target_service=$2 AND world_id=$3 AND channel_id=$4 AND consumed_at IS NULL AND expires_at<=clock_timestamp()`, handle, target, session.Lease.WorldID, channel); err != nil {
		return domain.CashTransfer{}, err
	}
	transfer := domain.CashTransfer{ID: id, NativeHandle: handle, WorldID: session.Lease.WorldID, ChannelID: channel}
	err = tx.QueryRowContext(ctx, `INSERT INTO ops_service_transfers(id,native_handle,target_service,account_id,character_id,world_id,channel_id,session_id,generation,client_ip,target_instance,target_epoch,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,clock_timestamp()+interval '30 seconds') RETURNING expires_at`, id, handle, target, session.AccountID, session.CharacterID, session.Lease.WorldID, channel, session.SessionID, session.Generation+1, ip, instance, epoch).Scan(&transfer.ExpiresAt)
	if err != nil {
		return transfer, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE ops_account_sessions SET state='transfer',generation=generation+1,owner_instance=$1,world_epoch=$2,expires_at=$3 WHERE account_id=$4 AND session_id=$5 AND generation=$6`, instance, epoch, transfer.ExpiresAt, session.AccountID, session.SessionID, session.Generation)
	if err != nil {
		return transfer, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return transfer, domain.ErrStaleOwner
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM ops_character_ownership WHERE character_id=$1 AND session_id=$2 AND generation=$3`, session.CharacterID, session.SessionID, session.Generation)
	return transfer, err
}

func (s *Store) ConsumeCashTransfer(ctx context.Context, handle int64, ip string, l domain.ServiceLease) (domain.CashSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashSession{}, err
	}
	defer tx.Rollback()
	if err = guardCashLease(ctx, tx, l); err != nil {
		return domain.CashSession{}, err
	}
	// The pending ticket is unique per handle and target, so its stage can be
	// read before consumeServiceTicket checks and consumes it.
	var stage string
	if err = tx.QueryRowContext(ctx, `SELECT stage FROM ops_service_transfers WHERE native_handle=$1 AND target_service='cashshop' AND consumed_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE`, handle).Scan(&stage); err != nil {
		return domain.CashSession{}, domain.ErrTransferInvalid
	}
	session, channel, err := consumeServiceTicket(ctx, tx, "cashshop", handle, -1, 0, ip, l.InstanceID, l.Epoch, l.ExpiresAt)
	if err != nil {
		return domain.CashSession{}, err
	}
	var accountName string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM auth_accounts WHERE id=$1`, session.AccountID).Scan(&accountName); err != nil {
		return domain.CashSession{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.CashSession{}, err
	}
	return domain.CashSession{AccountName: accountName, Session: session, Lease: l, NativeHandle: handle, ReturnChannel: channel, ITC: stage == "itc"}, nil
}

func (s *Store) BeginCashReturn(ctx context.Context, c domain.CashSession, ip string) (domain.CashTransfer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CashTransfer{}, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, c); err != nil {
		return domain.CashTransfer{}, err
	}
	var instance string
	var epoch int64
	if err = tx.QueryRowContext(ctx, `SELECT instance_id,epoch FROM ops_runtime_leases WHERE world_id=$1 AND expires_at>clock_timestamp() FOR SHARE`, c.Session.Lease.WorldID).Scan(&instance, &epoch); err != nil {
		return domain.CashTransfer{}, domain.ErrStaleOwner
	}
	transfer, err := createServiceTicket(ctx, tx, c.Session, c.NativeHandle, ip, "world", c.ReturnChannel, instance, epoch)
	if err != nil {
		return transfer, err
	}
	return transfer, tx.Commit()
}

// ConsumeCashReturn claims a live cashshop return ticket. An expired ticket
// that was never used does not match, so the next fresh login of the same
// native handle to that channel goes through its own login transfer.
func (s *Store) ConsumeCashReturn(ctx context.Context, handle int64, world, channel int, ip string, l domain.WorldLease) (domain.CharacterSession, bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ops_service_transfers WHERE native_handle=$1 AND target_service='world' AND world_id=$2 AND channel_id=$3 AND consumed_at IS NULL AND expires_at>clock_timestamp())`, handle, world, channel).Scan(&exists); err != nil {
		return domain.CharacterSession{}, false, err
	}
	if !exists {
		return domain.CharacterSession{}, false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.CharacterSession{}, true, err
	}
	defer tx.Rollback()
	if err = guardLease(ctx, tx, l); err != nil {
		return domain.CharacterSession{}, true, err
	}
	session, _, err := consumeServiceTicket(ctx, tx, "world", handle, world, channel, ip, l.InstanceID, l.Epoch, l.ExpiresAt)
	if err != nil {
		return domain.CharacterSession{}, true, err
	}
	return session, true, tx.Commit()
}

func consumeServiceTicket(ctx context.Context, tx *sql.Tx, target string, handle int64, world, channel int, ip, instance string, epoch int64, leaseExpiry time.Time) (domain.CharacterSession, int, error) {
	if !domain.TrustedClientIP(ip) {
		return domain.CharacterSession{}, 0, domain.ErrTransferInvalid
	}
	var id, sessionID, ticketIP, ticketInstance string
	var accountID, characterID, generation, ticketEpoch int64
	var ticketWorld, ticketChannel int
	err := tx.QueryRowContext(ctx, `SELECT id,account_id,character_id,world_id,channel_id,session_id,generation,client_ip,target_instance,target_epoch FROM ops_service_transfers WHERE native_handle=$1 AND target_service=$2 AND ($3<0 OR world_id=$3) AND ($4=0 OR channel_id=$4) AND consumed_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE`, handle, target, world, channel).Scan(&id, &accountID, &characterID, &ticketWorld, &ticketChannel, &sessionID, &generation, &ticketIP, &ticketInstance, &ticketEpoch)
	if err != nil || ticketIP != ip || ticketInstance != instance || ticketEpoch != epoch {
		return domain.CharacterSession{}, 0, domain.ErrTransferInvalid
	}
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM ops_account_sessions WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='transfer' AND owner_instance=$4 AND world_epoch=$5 AND character_id=$6 AND world_id=$7 AND expires_at>clock_timestamp() FOR UPDATE`, accountID, sessionID, generation, instance, epoch, characterID, ticketWorld).Scan(&current)
	if err != nil {
		return domain.CharacterSession{}, 0, domain.ErrTransferInvalid
	}
	state := target
	if _, err = tx.ExecContext(ctx, `UPDATE ops_account_sessions SET state=$1,generation=generation+1,expires_at=$2 WHERE account_id=$3`, state, leaseExpiry, accountID); err != nil {
		return domain.CharacterSession{}, 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE ops_service_transfers SET consumed_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return domain.CharacterSession{}, 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_character_ownership(character_id,account_id,world_id,instance_id,epoch,session_id,generation,channel_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(character_id) DO UPDATE SET account_id=EXCLUDED.account_id,world_id=EXCLUDED.world_id,instance_id=EXCLUDED.instance_id,epoch=EXCLUDED.epoch,session_id=EXCLUDED.session_id,generation=EXCLUDED.generation,channel_id=EXCLUDED.channel_id`, characterID, accountID, ticketWorld, instance, epoch, sessionID, generation+1, ticketChannel); err != nil {
		return domain.CharacterSession{}, 0, err
	}
	c, err := readCharacterSQL(ctx, tx, characterID)
	if err != nil {
		return domain.CharacterSession{}, 0, err
	}
	return domain.CharacterSession{AccountID: accountID, CharacterID: characterID, SessionID: sessionID, Generation: generation + 1, ChannelID: ticketChannel, Lease: domain.WorldLease{WorldID: ticketWorld, InstanceID: instance, Epoch: epoch, ExpiresAt: leaseExpiry}, Character: c}, ticketChannel, nil
}

func (s *Store) ReleaseCashCharacter(ctx context.Context, c domain.CashSession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE ops_account_sessions SET state='offline',expires_at=clock_timestamp() WHERE account_id=$1 AND session_id=$2 AND generation=$3 AND state='cashshop' AND owner_instance=$4 AND world_epoch=$5`, c.Session.AccountID, c.Session.SessionID, c.Session.Generation, c.Lease.InstanceID, c.Lease.Epoch); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM ops_character_ownership WHERE character_id=$1 AND session_id=$2 AND generation=$3 AND instance_id=$4 AND epoch=$5`, c.Session.CharacterID, c.Session.SessionID, c.Session.Generation, c.Lease.InstanceID, c.Lease.Epoch); err != nil {
		return err
	}
	return tx.Commit()
}
