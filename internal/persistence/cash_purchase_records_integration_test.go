package persistence

import (
	"errors"
	"fmt"
	"testing"

	"maple-go-server/internal/domain"
)

func TestCashPurchaseRecordUsesCommittedAccountHistory(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	lease := cashTestLease(t, ctx, s, actors[0].CharacterID)
	var cash [2]domain.CashSession
	for i, actor := range actors {
		handle := actor.CharacterID + 100000
		if _, err := s.BeginCashTransfer(ctx, actor, handle, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		var err error
		cash[i], err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ReadCashWallet(ctx, cash[i]); err != nil {
			t.Fatal(err)
		}
	}
	check := func(session domain.CashSession, sn int, want bool) {
		t.Helper()
		got, err := s.ReadCashPurchaseRecord(ctx, session, sn)
		if err != nil || got != want {
			t.Fatalf("sn%d got%v want%v: %v", sn, got, want, err)
		}
	}
	check(cash[0], 0, false)
	check(cash[0], 10000049, false)
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=1000 WHERE account_id=$1`, cash[0].Session.AccountID); err != nil {
		t.Fatal(err)
	}
	request := domain.CashPurchaseRequest{Session: cash[0], OperationID: fmt.Sprintf("history-purchase-%d", cash[0].Session.CharacterID), SN: 10000049, Currency: 1}
	got, err := s.PurchaseCash(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	check(cash[0], 10000049, true)
	check(cash[0], 0, true)
	check(cash[0], 12345678, false)
	check(cash[1], 0, false)
	check(cash[1], 10000049, false)
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_items SET state='expired' WHERE id=$1`, got.Item.ID); err != nil {
		t.Fatal(err)
	}
	check(cash[0], 10000049, true)
	request.OperationID += "-invalid"
	request.SN = 1
	if _, err = s.PurchaseCash(ctx, request); err == nil {
		t.Fatal("invalid product purchased")
	}
	check(cash[0], 1, false)
	stale := cash[0]
	stale.Session.Generation++
	if _, err = s.ReadCashPurchaseRecord(ctx, stale, 0); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale history read: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=1000 WHERE account_id=$1`, cash[1].Session.AccountID); err != nil {
		t.Fatal(err)
	}
	_, err = s.ChangeCashFeature(ctx, domain.CashFeatureRequest{Session: cash[1], Kind: "gift", OperationID: fmt.Sprintf("history-gift-command-%d", cash[1].Session.CharacterID), SN: 10000049, PIN: "145145", Recipient: cash[0].Session.Character.Name, Message: "History test"})
	if err != nil {
		t.Fatal(err)
	}
	check(cash[1], 10000049, true)
	check(cash[1], 0, true)
}
