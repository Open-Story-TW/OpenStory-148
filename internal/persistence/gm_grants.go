package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maple-go-server/internal/domain"
	"strings"
	"time"
)

func gmDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validGMOperation(id string) bool {
	return len(id) >= 16 && len(id) <= 128 && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "\x00\r\n")
}
func readQAGMGrant(ctx context.Context, tx *sql.Tx, cid int64) (domain.QAGMGrant, error) {
	var g domain.QAGMGrant
	err := tx.QueryRowContext(ctx, `SELECT account_id,character_id,world_id,role,expires_at,enabled,revision FROM ops_qa_gm_grants WHERE character_id=$1 FOR SHARE`, cid).Scan(&g.AccountID, &g.CharacterID, &g.WorldID, &g.Role, &g.ExpiresAt, &g.Enabled, &g.Revision)
	return g, err
}
func validQAGMGrant(g domain.QAGMGrant, c domain.Character, now time.Time) bool {
	return c.GM == domain.QAGMRank && g.AccountID == c.AccountID && g.CharacterID == c.ID && g.WorldID == c.WorldID && g.Role == domain.QAGMRole && g.Enabled && g.ExpiresAt.After(now)
}

func (s *Store) AuthorizeQAGM(ctx context.Context, r domain.QAGMGrantRequest) (domain.QAGMGrantResult, error) {
	if !validGMOperation(r.OperationID) || r.AccountID < 1 || r.CharacterID < 1 || r.WorldID < 0 || r.WorldID > 127 || r.Role != domain.QAGMRole || (r.Action != "grant" && r.Action != "revoke") || len(strings.TrimSpace(r.Actor)) < 1 || len(r.Actor) > 128 || len(strings.TrimSpace(r.Reason)) < 1 || len(r.Reason) > 512 {
		return domain.QAGMGrantResult{}, domain.ErrInvalidInput
	}
	if r.Permanent {
		r.ExpiresAt = domain.QAGMPermanentExpiry
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QAGMGrantResult{}, err
	}
	defer tx.Rollback()
	// The parent row lock prevents a first account-session insert (its FK key
	// lock conflicts). An existing account-session row is then locked in the
	// same account-before-character order as world authority transactions.
	var account int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM auth_accounts WHERE id=$1 FOR UPDATE`, r.AccountID).Scan(&account); err != nil {
		return domain.QAGMGrantResult{}, err
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM ops_account_sessions WHERE account_id=$1 FOR UPDATE`, r.AccountID).Scan(&state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.QAGMGrantResult{}, err
	}
	var cid int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM game_characters WHERE id=$1 AND account_id=$2 AND world_id=$3 FOR UPDATE`, r.CharacterID, r.AccountID, r.WorldID).Scan(&cid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.ErrNotFound
		}
		return domain.QAGMGrantResult{}, err
	}
	hash := gmDigest(struct {
		Actor   string
		Request domain.QAGMGrantRequest
	}{r.Actor, r})
	var priorHash string
	var priorRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM ops_qa_gm_authorizations WHERE operation_id=$1`, r.OperationID).Scan(&priorHash, &priorRaw)
	if err == nil {
		if priorHash != hash {
			return domain.QAGMGrantResult{}, domain.ErrForbidden
		}
		g, e := readQAGMGrant(ctx, tx, cid)
		return domain.QAGMGrantResult{Grant: g, Replayed: true}, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.QAGMGrantResult{}, err
	}
	if r.Action == "grant" && state != "" && state != "offline" {
		return domain.QAGMGrantResult{}, domain.ErrAlreadyExists
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return domain.QAGMGrantResult{}, err
	}
	if r.Action == "grant" && !r.Permanent && (!r.ExpiresAt.After(now) || r.ExpiresAt.After(now.Add(24*time.Hour))) {
		return domain.QAGMGrantResult{}, domain.ErrInvalidInput
	}
	c, err := readCharacterSQL(ctx, tx, cid)
	if err != nil {
		return domain.QAGMGrantResult{}, err
	}
	previous, err := readQAGMGrant(ctx, tx, cid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.QAGMGrantResult{}, err
	}
	if previous.CharacterID != 0 && (previous.AccountID != r.AccountID || previous.WorldID != r.WorldID || previous.Role != domain.QAGMRole) {
		return domain.QAGMGrantResult{}, domain.ErrForbidden
	}
	if c.GM != 0 && (c.GM != domain.QAGMRank || previous.CharacterID == 0) {
		return domain.QAGMGrantResult{}, domain.ErrForbidden
	}
	enabled := r.Action == "grant"
	expiry := r.ExpiresAt
	if !enabled {
		if previous.CharacterID == 0 {
			return domain.QAGMGrantResult{}, domain.ErrNotFound
		}
		expiry = previous.ExpiresAt
	}
	result := domain.QAGMGrantResult{Grant: domain.QAGMGrant{AccountID: r.AccountID, CharacterID: cid, WorldID: r.WorldID, Role: r.Role, ExpiresAt: expiry, Enabled: enabled, Revision: previous.Revision + 1}}
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_qa_gm_grants(character_id,account_id,world_id,role,expires_at,enabled,revision,authorized_by,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(character_id) DO UPDATE SET expires_at=EXCLUDED.expires_at,enabled=EXCLUDED.enabled,revision=EXCLUDED.revision,authorized_by=EXCLUDED.authorized_by,reason=EXCLUDED.reason,updated_at=clock_timestamp()`, cid, r.AccountID, r.WorldID, r.Role, expiry, enabled, result.Grant.Revision, r.Actor, r.Reason)
	if err != nil {
		return result, err
	}
	c.GM = 0
	if enabled {
		c.GM = domain.QAGMRank
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_characters SET gm=$1 WHERE id=$2`, c.GM, cid); err != nil {
		return result, err
	}
	roster, err := marshalRoster(c)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE game_character_roster SET snapshot=$1 WHERE character_id=$2`, roster, cid); err != nil {
		return result, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_qa_gm_authorizations(operation_id,request_hash,actor,action,account_id,character_id,world_id,reason,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, r.OperationID, hash, r.Actor, r.Action, r.AccountID, cid, r.WorldID, r.Reason, raw); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ops_outbox(event_type,account_id,world_id) VALUES('roster.invalidate',$1,$2)`, r.AccountID, r.WorldID); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	s.roster.Invalidate(ctx, r.AccountID, r.WorldID)
	return result, nil
}
