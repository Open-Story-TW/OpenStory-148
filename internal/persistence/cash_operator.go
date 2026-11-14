package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

var _ domain.CashCouponOperatorRepository = (*Store)(nil)

// IssueCashCoupon creates an account-bound credential and an immutable audit
// receipt. It does not create currency or items; the native redemption command
// validates and consumes the coupon in the same transaction as the grant.
func (s *Store) IssueCashCoupon(ctx context.Context, r domain.CashCouponIssueRequest) (domain.CashCouponIssueResult, error) {
	r.Code = strings.TrimSpace(r.Code)
	r.Actor = strings.TrimSpace(r.Actor)
	if len(r.OperationID) < 16 || len(r.OperationID) > 128 || r.Actor == "" || len(r.Actor) > 128 || !accountPattern.MatchString(r.AccountName) || len(r.Code) < 16 || len(r.Code) > 64 || r.Value < 1 || r.Value > maxMesos || r.ExpiresAt.IsZero() {
		return domain.CashCouponIssueResult{}, domain.ErrInvalidInput
	}
	switch r.Kind {
	case "credit", "points", "mesos":
	case "item":
		p, err := content.CashProduct(int(r.Value))
		if err != nil || p.TemplateID/10000 == 500 {
			return domain.CashCouponIssueResult{}, domain.ErrInvalidInput
		}
		if _, err = inventoryDefinition(p.TemplateID); err != nil {
			return domain.CashCouponIssueResult{}, domain.ErrInvalidInput
		}
	default:
		return domain.CashCouponIssueResult{}, domain.ErrInvalidInput
	}
	result := domain.CashCouponIssueResult{OperationID: r.OperationID, Actor: r.Actor, CodeHash: tokenHash(r.Code), Kind: r.Kind, Value: r.Value, ExpiresAt: r.ExpiresAt.UTC()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id,name FROM auth_accounts WHERE name_key=$1 FOR KEY SHARE`, strings.ToLower(r.AccountName)).Scan(&result.AccountID, &result.AccountName)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CashCouponIssueResult{}, domain.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	// No plaintext code enters the audit JSON, including its request hash input.
	requestRaw, _ := json.Marshal(result)
	hash := tokenHash(string(requestRaw))
	var priorHash string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM asset_cash_feature_operations WHERE operation_id=$1`, r.OperationID).Scan(&priorHash, &raw)
	if err == nil {
		if hash != priorHash {
			return domain.CashCouponIssueResult{}, domain.ErrForbidden
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
		result.Replayed = true
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT $1::timestamptz>clock_timestamp() AND $1::timestamptz<=clock_timestamp()+interval '365 days'`, result.ExpiresAt).Scan(&active); err != nil {
		return result, err
	}
	if !active {
		return domain.CashCouponIssueResult{}, domain.ErrInvalidInput
	}
	created, err := tx.ExecContext(ctx, `INSERT INTO asset_cash_coupons(code_hash,kind,value,account_id,expires_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(code_hash) DO NOTHING`, result.CodeHash, result.Kind, result.Value, result.AccountID, result.ExpiresAt)
	if err != nil {
		return result, err
	}
	if n, _ := created.RowsAffected(); n != 1 {
		return domain.CashCouponIssueResult{}, domain.ErrAlreadyExists
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO asset_cash_feature_operations(operation_id,account_id,request_hash,result) VALUES($1,$2,$3,$4)`, result.OperationID, result.AccountID, hash, raw); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
