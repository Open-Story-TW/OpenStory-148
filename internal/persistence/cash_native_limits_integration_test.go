package persistence

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"maple-go-server/internal/domain"
)

func TestNativeCashLimitsAreEnforcedInsideWalletTransaction(t *testing.T) {
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
		if _, err = s.db.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=1000 WHERE account_id=$1`, actor.AccountID); err != nil {
			t.Fatal(err)
		}
	}
	buy := func(i, sn int, op string) (domain.CashPurchaseResult, error) {
		return s.PurchaseCash(ctx, domain.CashPurchaseRequest{Session: cash[i], SN: sn, Currency: 1, OperationID: fmt.Sprintf("native-limit-%d-%s", cash[i].Session.CharacterID, op)})
	}
	if _, err := buy(0, 10000049, "prior"); err != nil {
		t.Fatal(err)
	}
	if _, err := buy(0, 10003366, "not-first"); !errors.Is(err, domain.ErrCashLimit) {
		t.Fatalf("first-purchase restriction bypassed: %v", err)
	}
	if _, err := s.PurchaseCash(ctx, domain.CashPurchaseRequest{Session: cash[1], SN: 10003366, Currency: 2, OperationID: fmt.Sprintf("native-limit-wrong-currency-%d", cash[1].Session.CharacterID)}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("first-purchase credit restriction bypassed: %v", err)
	}
	if _, err := buy(1, 10003366, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := buy(1, 10003366, "second-first"); !errors.Is(err, domain.ErrCashLimit) {
		t.Fatalf("first offer repeated: %v", err)
	}
	var wg sync.WaitGroup
	var wins atomic.Int32
	var winning atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := buy(1, 10003386, fmt.Sprintf("single-%d", i))
			if err == nil {
				wins.Add(1)
				winning.Store(int32(i))
			} else if !errors.Is(err, domain.ErrCashLimit) {
				t.Errorf("single-SN race: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("single-SN restriction allowed %d purchases", wins.Load())
	}
	replay, err := buy(1, 10003386, fmt.Sprintf("single-%d", winning.Load()))
	if err != nil || !replay.Replayed {
		t.Fatalf("receipt replay blocked by purchase restriction: %+v %v", replay, err)
	}
	wallet, err := s.ReadCashWallet(ctx, cash[1])
	if err != nil || wallet.Credit != 998 {
		t.Fatalf("extra charge: %+v %v", wallet, err)
	}
	_, err = s.ChangeCashFeature(ctx, domain.CashFeatureRequest{Session: cash[1], Kind: "gift", OperationID: fmt.Sprintf("native-limit-gift-%d", cash[1].Session.CharacterID), SN: 10003386, PIN: "145145", Recipient: cash[0].Session.Character.Name, Message: "Limit check"})
	if !errors.Is(err, domain.ErrCashLimit) {
		t.Fatalf("gift bypassed single-SN limit: %v", err)
	}
}
