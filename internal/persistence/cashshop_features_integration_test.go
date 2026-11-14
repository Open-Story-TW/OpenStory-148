package persistence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"maple-go-server/internal/domain"
)

func TestCashWishlistGiftCouponAndPackageAtomicity(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	lease := cashTestLease(t, ctx, s, sessions[0].CharacterID)
	var cash [2]domain.CashSession
	for i, a := range sessions {
		handle := a.CharacterID + 100000
		if _, err := s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
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
	root := t.TempDir()
	for file, item := range map[string]int{"Accessory/01022009.img.xml": 1022009, "Longcoat/01052367.img.xml": 1052367, "Shoes/01072537.img.xml": 1072537, "Cap/01002434.img.xml": 1002434} {
		path := filepath.Join(root, "Character.wz", filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`<imgdir name="%08d.img"><imgdir name="info"><int name="cash" value="1"/><int name="tuc" value="0"/></imgdir></imgdir>`, item)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	if _, err := s.db.ExecContext(ctx, `UPDATE asset_cash_wallets SET credit=1000 WHERE account_id=$1`, cash[0].Session.AccountID); err != nil {
		t.Fatal(err)
	}
	op := func(kind string) domain.CashFeatureRequest {
		return domain.CashFeatureRequest{Session: cash[0], Kind: kind, OperationID: fmt.Sprintf("cash-feature-%d-%s", cash[0].Session.CharacterID, kind)}
	}
	wish := op("wishlist")
	wish.Wishlist = [10]int{10000049, 10000881}
	if _, err := s.ChangeCashFeature(ctx, wish); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadCashWishlist(ctx, cash[0]); err != nil || got != wish.Wishlist {
		t.Fatalf("wishlist=%v %v", got, err)
	}
	if got, err := s.ChangeCashFeature(ctx, wish); err != nil || !got.Replayed {
		t.Fatalf("wishlist replay=%+v %v", got, err)
	}
	gift := op("gift")
	gift.SN = 10000049
	gift.PIN = "wrong"
	gift.Recipient = cash[1].Session.Character.Name
	gift.Message = "雙人禮物測試"
	if _, err := s.ChangeCashFeature(ctx, gift); !errors.Is(err, domain.ErrBadCredentials) {
		t.Fatalf("gift PIN accepted %v", err)
	}
	gift.PIN = "145145"
	sent, err := s.ChangeCashFeature(ctx, gift)
	if err != nil || sent.Wallet.Credit != 945 {
		t.Fatalf("gift=%+v %v", sent, err)
	}
	if replay, err := s.ChangeCashFeature(ctx, gift); err != nil || !replay.Replayed || replay.Wallet.Credit != 945 {
		t.Fatalf("gift replay=%+v %v", replay, err)
	}
	gifts, err := s.ClaimCashGifts(ctx, cash[1])
	if err != nil || len(gifts) != 1 || gifts[0].Item.TemplateID != 1022009 || gifts[0].Message != gift.Message {
		t.Fatalf("gifts=%+v %v", gifts, err)
	}
	if again, err := s.ClaimCashGifts(ctx, cash[1]); err != nil || len(again) != 0 {
		t.Fatalf("gift delivered twice %+v %v", again, err)
	}
	locker, err := s.ReadCashLocker(ctx, cash[1])
	if err != nil || len(locker) != 1 || locker[0].GiftFrom != cash[0].Session.Character.Name {
		t.Fatalf("gift sender missing %+v %v", locker, err)
	}
	pack := op("package")
	pack.SN = 10003229 // Source package: 1052367 + 1072537; 135 NX.
	bought, err := s.ChangeCashFeature(ctx, pack)
	if err != nil || len(bought.Items) != 2 || bought.Wallet.Credit != 810 {
		t.Fatalf("package=%+v %v", bought, err)
	}
	if replay, err := s.ChangeCashFeature(ctx, pack); err != nil || !replay.Replayed || len(replay.Items) != 2 || replay.Items[0].ID != bought.Items[0].ID {
		t.Fatalf("package replay=%+v %v", replay, err)
	}
	// Coupon issuance is test setup only; production has no automatic minting.
	for kind, value := range map[string]int64{"credit": 100, "item": 10000049, "mesos": 77} {
		code := fmt.Sprintf("fixture-%d-%s", cash[0].Session.CharacterID, kind)
		if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_cash_coupons(code_hash,kind,value,account_id,expires_at) VALUES($1,$2,$3,$4,clock_timestamp()+interval '1 hour')`, tokenHash(code), kind, value, cash[0].Session.AccountID); err != nil {
			t.Fatal(err)
		}
		r := op("coupon")
		r.Coupon = code
		r.OperationID += "-" + kind
		foreign := r
		foreign.Session = cash[1]
		foreign.OperationID += "-foreign"
		if _, err = s.ChangeCashFeature(ctx, foreign); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("foreign coupon accepted %v", err)
		}
		got, err := s.ChangeCashFeature(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if kind == "credit" && got.Wallet.Credit != 910 {
			t.Fatalf("coupon credit=%d", got.Wallet.Credit)
		}
		if kind == "item" && (len(got.Items) != 1 || len(got.Changes) != 1 || got.Items[0].State != "inventory") {
			t.Fatalf("coupon item=%+v", got)
		}
		if kind == "mesos" && got.Character.Mesos != cash[0].Session.Character.Mesos+77 {
			t.Fatalf("coupon mesos=%+v", got)
		}
		r.OperationID += "-again"
		if _, err = s.ChangeCashFeature(ctx, r); !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("coupon double redeemed %v", err)
		}
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_cash_limits(sn,account_limit) VALUES(10000881,1)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.db.ExecContext(ctx, `DELETE FROM asset_cash_limits WHERE sn=10000881`) })
	limit := domain.CashPurchaseRequest{Session: cash[0], OperationID: fmt.Sprintf("cash-limited-%d-first", cash[0].Session.CharacterID), SN: 10000881, Currency: 1}
	if _, err = s.PurchaseCash(ctx, limit); err != nil {
		t.Fatal(err)
	}
	limit.OperationID += "-second"
	if _, err = s.PurchaseCash(ctx, limit); !errors.Is(err, domain.ErrCashLimit) {
		t.Fatalf("account limit bypassed %v", err)
	}
	// Full locker: no partial package and no debit after failed delivery.
	if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,state) SELECT $1,$2,1,5072000,1,5,'locker' FROM generate_series(1,96)`, cash[0].Session.AccountID, cash[0].Session.Lease.WorldID); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadCashWallet(ctx, cash[0])
	if err != nil {
		t.Fatal(err)
	}
	pack.OperationID += "-full"
	if _, err = s.ChangeCashFeature(ctx, pack); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatalf("full package accepted %v", err)
	}
	after, err := s.ReadCashWallet(ctx, cash[0])
	if err != nil || after != before {
		t.Fatalf("failed package charged %+v -> %+v err%v", before, after, err)
	}
}
