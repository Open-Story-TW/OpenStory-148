package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestCashOperatorIssuesAuditedBoundCouponWithoutFunding(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM auth_accounts WHERE id=$1`, a.AccountID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	req := domain.CashCouponIssueRequest{OperationID: fmt.Sprintf("operator-coupon-issue-%d", a.AccountID), Actor: "local-operator", AccountName: strings.ToUpper(name), Code: "1234567890123:9876543210", Kind: "credit", Value: 1000, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	issued, err := s.IssueCashCoupon(ctx, req)
	if err != nil || issued.AccountID != a.AccountID || issued.CodeHash != tokenHash(req.Code) || issued.Actor != "local-operator" {
		t.Fatalf("issue=%+v %v", issued, err)
	}
	if replay, err := s.IssueCashCoupon(ctx, req); err != nil || !replay.Replayed || replay.CodeHash != issued.CodeHash {
		t.Fatalf("issue replay=%+v %v", replay, err)
	}
	changed := req
	changed.Value++
	if _, err = s.IssueCashCoupon(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("replayed issue changed reward %v", err)
	}
	var audit []byte
	if err = s.db.QueryRowContext(ctx, `SELECT result FROM asset_cash_feature_operations WHERE operation_id=$1`, req.OperationID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(issued)
	if strings.Contains(string(audit), req.Code) || strings.Contains(string(encoded), req.Code) {
		t.Fatal("coupon credential retained in receipt")
	}
	lease := cashTestLease(t, ctx, s, a.CharacterID)
	var cash [2]domain.CashSession
	for i, session := range sessions {
		handle := session.CharacterID + 100000
		if _, err = s.BeginCashTransfer(ctx, session, handle, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		cash[i], err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
		if err != nil {
			t.Fatal(err)
		}
	}
	if wallet, err := s.ReadCashWallet(ctx, cash[0]); err != nil || wallet.Credit != 0 {
		t.Fatalf("issuance funded wallet=%+v %v", wallet, err)
	}
	// Exercise the native coupon parser before the durable redemption command.
	w := protocol.NewRawWriter()
	w.U8(0)
	w.String("1234567890123")
	w.String("9876543210")
	w.U8(0)
	redeem, err := protocol.ReadNativeCashCoupon(protocol.NewReader(w.Packet()))
	if err != nil {
		t.Fatal(err)
	}
	redeem.Session = cash[1]
	redeem.OperationID = fmt.Sprintf("operator-coupon-foreign-%d", a.AccountID)
	if _, err = s.ChangeCashFeature(ctx, redeem); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("foreign account redeemed bound coupon %v", err)
	}
	redeem.Session = cash[0]
	redeem.OperationID = fmt.Sprintf("operator-coupon-redeem-%d", a.AccountID)
	got, err := s.ChangeCashFeature(ctx, redeem)
	if err != nil || got.Wallet.Credit != 1000 {
		t.Fatalf("native coupon redeem=%+v %v", got, err)
	}
	if replay, err := s.ChangeCashFeature(ctx, redeem); err != nil || !replay.Replayed || replay.Wallet.Credit != 1000 {
		t.Fatalf("redemption replay=%+v %v", replay, err)
	}
	// Issuance replay after redemption must never rearm a consumed credential.
	if _, err = s.IssueCashCoupon(ctx, req); err != nil {
		t.Fatal(err)
	}
	redeem.OperationID += "-again"
	if _, err = s.ChangeCashFeature(ctx, redeem); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("issue replay rearmed consumed coupon %v", err)
	}
}
