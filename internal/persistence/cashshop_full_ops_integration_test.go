package persistence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestCashExpiryKeepsSafetyCharmJavaPeriod(t *testing.T) {
	charm := cashExpiry(domain.CashProduct{TemplateID: 5320000})
	if days := time.Until(time.UnixMilli(charm)).Hours() / 24; days < 89.9 || days > 90.1 {
		t.Fatalf("safety charm expires in %.2f days", days)
	}
	if cashExpiry(domain.CashProduct{TemplateID: 1022009}) != 0 {
		t.Fatal("permanent product received an expiry")
	}
}

// Gift, gift-package and locker expiry against a private PostgreSQL schema.
func TestCashGiftPackageAndLockerExpiry(t *testing.T) {
	useCombatSchema(t)
	ctx, s, sessions := inventoryFixture(t)
	lease := cashTestLease(t, ctx, s, sessions[0].CharacterID)
	var cash [2]domain.CashSession
	for i, a := range sessions {
		handle := a.CharacterID + 200000
		if _, err := s.BeginCashTransfer(ctx, a, handle, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		var err error
		if cash[i], err = s.ConsumeCashTransfer(ctx, handle, "127.0.0.1", lease); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ReadCashWallet(ctx, cash[i]); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	for file, item := range map[string]int{"Accessory/01022009.img.xml": 1022009, "Longcoat/01052367.img.xml": 1052367, "Shoes/01072537.img.xml": 1072537, "Pants/01061130.img.xml": 1061130} {
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
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET gender=0 WHERE id=$1`, cash[1].Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	seq := 0
	gift := func(kind string, sn int, recipient string) domain.CashFeatureRequest {
		seq++
		return domain.CashFeatureRequest{Session: cash[0], Kind: kind, SN: sn, PIN: "145145", Recipient: recipient, Message: "全套禮物", OperationID: fmt.Sprintf("cash-full-ops-%d-op%04d", cash[0].Session.CharacterID, seq)}
	}
	wallet := func() int64 {
		w, err := s.ReadCashWallet(ctx, cash[0])
		if err != nil {
			t.Fatal(err)
		}
		return w.Credit
	}
	// Refusals carry the reason the native notice needs and never charge.
	refusals := []struct {
		request domain.CashFeatureRequest
		want    error
	}{
		{gift("gift", 10000049, cash[0].Session.Character.Name), domain.ErrCashSameAccount},
		{gift("gift", 10000049, "NoSuchCharacterXYZ"), domain.ErrNotFound},
		{gift("gift", 10001145, cash[1].Session.Character.Name), domain.ErrCashGender},
	}
	for _, tc := range refusals {
		if _, err := s.ChangeCashFeature(ctx, tc.request); !errors.Is(err, tc.want) {
			t.Fatalf("gift to %q: %v, want %v", tc.request.Recipient, err, tc.want)
		}
	}
	if got := wallet(); got != 1000 {
		t.Fatalf("refused gifts charged: %d", got)
	}
	own := cash[0]
	own.Session.Character.Gender = 0
	if _, err := s.PurchaseCash(ctx, domain.CashPurchaseRequest{Session: own, OperationID: fmt.Sprintf("cash-full-%d-gender", own.Session.CharacterID), SN: 10001145, Currency: 1}); !errors.Is(err, domain.ErrCashGender) || !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("own gender purchase: %v", err)
	}
	// Gift-package: one charge, one notification, every member delivered.
	sent, err := s.ChangeCashFeature(ctx, gift("gift_package", 10003229, cash[1].Session.Character.Name))
	if err != nil || !sent.Package || sent.Wallet.Credit != 865 || sent.Recipient != cash[1].Session.Character.Name {
		t.Fatalf("gift package=%+v %v", sent, err)
	}
	// A pending gift that no longer validates must not block the recipient.
	if _, err = s.db.ExecContext(ctx, `INSERT INTO asset_cash_gifts(sender_account_id,sender_name,recipient_character_id,message,sn,template_id,products) VALUES($1,'Ghost',$2,'x',1,5000000,'[{"SN":1,"TemplateID":5000000,"Quantity":1,"Gender":2}]')`, cash[0].Session.AccountID, cash[1].Session.CharacterID); err != nil {
		t.Fatal(err)
	}
	gifts, err := s.ClaimCashGifts(ctx, cash[1])
	if err != nil || len(gifts) != 1 || gifts[0].Item.TemplateID != 9102511 || gifts[0].Sender != cash[0].Session.Character.Name {
		t.Fatalf("claimed=%+v %v", gifts, err)
	}
	locker, err := s.ReadCashLocker(ctx, cash[1])
	if err != nil || len(locker) != 2 {
		t.Fatalf("package members=%+v %v", locker, err)
	}
	for _, item := range locker {
		if item.GiftFrom != cash[0].Session.Character.Name || (item.TemplateID != 1052367 && item.TemplateID != 1072537) {
			t.Fatalf("package member=%+v", item)
		}
	}
	var pending int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM asset_cash_gifts WHERE recipient_character_id=$1 AND delivered_at IS NULL`, cash[1].Session.CharacterID).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("invalid gift should stay pending: %d %v", pending, err)
	}
	// Locker expiry retires each record once; pets keep their own lifetime.
	past := time.Now().Add(-time.Minute).UnixMilli()
	var expiredID, petID int64
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,state) VALUES($1,$2,1,5072000,1,5,$3,'locker') RETURNING id`, cash[0].Session.AccountID, cash[0].Session.Lease.WorldID, past).Scan(&expiredID); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRowContext(ctx, `INSERT INTO asset_cash_items(account_id,world_id,sn,template_id,quantity,inv_type,expires_at,state) VALUES($1,$2,1,5000000,1,5,$3,'locker') RETURNING id`, cash[0].Session.AccountID, cash[0].Session.Lease.WorldID, past).Scan(&petID); err != nil {
		t.Fatal(err)
	}
	expired, err := s.ExpireCashLocker(ctx, cash[0])
	if err != nil || len(expired) != 1 || expired[0].ID != expiredID || expired[0].State != "expired" {
		t.Fatalf("expired=%+v %v", expired, err)
	}
	if again, err := s.ExpireCashLocker(ctx, cash[0]); err != nil || len(again) != 0 {
		t.Fatalf("expiry announced twice %+v %v", again, err)
	}
	var petState string
	if err = s.db.QueryRowContext(ctx, `SELECT state FROM asset_cash_items WHERE id=$1`, petID).Scan(&petState); err != nil || petState != "locker" {
		t.Fatalf("pet state=%q %v", petState, err)
	}
	if _, err = s.ExpireCashLocker(ctx, cash[1]); err != nil {
		t.Fatal(err)
	}
	stale := cash[0]
	stale.Lease.Epoch++
	if _, err = s.ExpireCashLocker(ctx, stale); err == nil {
		t.Fatal("stale cash lease expired locker records")
	}
}
