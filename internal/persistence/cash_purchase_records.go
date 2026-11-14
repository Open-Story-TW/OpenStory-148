package persistence

import (
	"context"
	"database/sql"
	"strconv"

	"maple-go-server/internal/domain"
)

var _ domain.CashPurchaseHistoryRepository = (*Store)(nil)

func (s *Store) ReadCashPurchaseRecord(ctx context.Context, session domain.CashSession, sn int) (bool, error) {
	if sn < 0 || sn > 2147483647 {
		return false, domain.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = guardCashSession(ctx, tx, session); err != nil {
		return false, err
	}
	return readCashPurchaseRecordSQL(ctx, tx, session.Session.AccountID, sn)
}

func readCashPurchaseRecordSQL(ctx context.Context, tx *sql.Tx, accountID int64, sn int) (bool, error) {
	var purchased bool
	var err error
	if sn == 0 {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_cash_orders WHERE account_id=$1)
		OR EXISTS(SELECT 1 FROM asset_cash_feature_operations WHERE account_id=$1 AND result#>>'{Product,SN}' IS NOT NULL AND result#>>'{Product,SN}'<>'0')`, accountID).Scan(&purchased)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_cash_orders WHERE account_id=$1 AND sn=$2)
		OR EXISTS(SELECT 1 FROM asset_cash_feature_operations WHERE account_id=$1 AND result#>>'{Product,SN}'=$3 AND result#>>'{Product,SN}'<>'0')`, accountID, sn, strconv.Itoa(sn)).Scan(&purchased)
	}
	return purchased, err
}

// Call only after the account wallet row is locked, in the same transaction
// as the purchase receipt. UI filtering alone cannot enforce a purchase limit.
func checkNativeCashLimit(ctx context.Context, tx *sql.Tx, accountID int64, p domain.CashProduct) error {
	if p.LimitMode == 0 {
		return nil
	}
	if p.LimitMode != 2 && p.LimitMode != 3 {
		return domain.ErrInvalidInput
	}
	sn := p.SN
	if p.LimitMode == 2 {
		sn = 0
	}
	purchased, err := readCashPurchaseRecordSQL(ctx, tx, accountID, sn)
	if err != nil {
		return err
	}
	if purchased {
		return domain.ErrCashLimit
	}
	return nil
}
