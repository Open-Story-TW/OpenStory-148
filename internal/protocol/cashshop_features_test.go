package protocol

import (
	"bytes"
	"encoding/hex"
	"maple-go-server/internal/domain"
	"testing"
)

func TestCashFeatureNativeLayouts(t *testing.T) {
	list := [10]int{10000049, 10000881}
	for _, update := range []bool{false, true} {
		p := CashWishlistItems(list, update)
		r := NewReader(p)
		code := byte(99)
		if update {
			code = 101
		}
		if len(p) != 43 || r.U16() != SendCashOperation || r.U8() != code || r.I32() != 10000049 || r.I32() != 10000881 {
			t.Fatalf("wishlist=%x", p)
		}
	}
	item := domain.CashItem{ID: 12, AccountID: 3, TemplateID: 1022009, SN: 10000049, Quantity: 1}
	gifts := CashGiftItems([]domain.CashGift{{Item: item, Sender: "IV1", Message: "禮物"}})
	if len(gifts) != 106 {
		t.Fatalf("gift notification width=%d", len(gifts))
	}
	r := NewReader(gifts)
	if r.U16() != SendCashOperation || r.U8() != 97 || r.U16() != 1 || r.I64() != 12 || r.I32() != 1022009 {
		t.Fatal("gift identity")
	}
	packagePacket := CashPackageBought([]domain.CashItem{item, item})
	// Native 0x47ABB8 is dispatcher case157; its trailing int32 SN=0
	// means no conditional 8-byte time value follows.
	if len(packagePacket) != 144 || packagePacket[2] != 157 || packagePacket[3] != 2 {
		t.Fatalf("package width=%d", len(packagePacket))
	}
	coupon := CashCouponRedeemed(domain.CashFeatureResult{Items: []domain.CashItem{item}, CreditAward: 20, MesosAward: 30})
	if len(coupon) != 83 || coupon[2] != 105 || coupon[3] != 1 {
		t.Fatal("coupon packet layout")
	}
	r = NewReader(coupon[71:])
	if r.I32() != 20 || r.I32() != 0 || r.I32() != 30 {
		t.Fatal("coupon award amounts")
	}
	limit := NewReader(CashLimitFailure(10000049))
	if limit.U16() != SendCashOperation || limit.U8() != 104 || limit.U8() != 29 || limit.I32() != 10000049 {
		t.Fatal("limited product failure requires SN")
	}
	sent := CashGiftSent(domain.CashFeatureResult{Recipient: "Bob", Product: domain.CashProduct{TemplateID: 1022009, Quantity: 1, Price: 55}})
	r = NewReader(sent)
	if r.U16() != SendCashOperation || r.U8() != 110 || r.String() != "Bob" || r.I32() != 1022009 || r.U16() != 1 || r.Remaining() != 0 {
		t.Fatalf("gift receipt=%x", sent)
	}
}

func TestCashNativeDecoderGoldens(t *testing.T) {
	// The golden bytes are the stock four-byte balance layout.
	defer domain.SetLongMoneyForTest(false)()
	// These byte boundaries follow the IDB v145 decoder at each address,
	// independently of Legacy Java's obsolete operation-number offsets.
	for _, tc := range []struct {
		name, decoder, want string
		got                 []byte
	}{
		{"wallet", "0x47D1E4", "60020a00000014000000", CashWalletPacket(domain.CashWallet{Credit: 10, Points: 20, Prepaid: 30})},
		{"gift", "0x47AEFA", "61026e0300426f6239980f000100", CashGiftSent(domain.CashFeatureResult{Recipient: "Bob", Product: domain.CashProduct{TemplateID: 1022009, Quantity: 1, Price: 55}})},
		{"giftpackage", "0x47AD9A", "61029f0300426f6239980f0001000000", CashGiftSent(domain.CashFeatureResult{Package: true, Recipient: "Bob", Product: domain.CashProduct{TemplateID: 1022009, Quantity: 1, Price: 55}})},
		{"package", "0x47ABB8", "61029d00000000000000", CashPackageBought(nil)},
		{"coupon", "0x47D8CB", "6102690014000000000000001e000000", CashCouponRedeemed(domain.CashFeatureResult{CreditAward: 20, MesosAward: 30})},
		{"purchaserecord", "0x47A6E4", "6102b20000000001", CashPurchaseRecord(0, true)},
		{"insufficient", "0x47D458", "61026803", CashFailure(3)},
		{"expired", "0x47B1D5", "6102803930000000000000", CashItemExpired(12345)},
	} {
		want, err := hex.DecodeString(tc.want)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(tc.got, want) {
			t.Fatalf("%s decoder%s got%x want%x", tc.name, tc.decoder, tc.got, want)
		}
	}
	if p := CashToLocker(domain.CashItem{}); len(p) != 70 || p[2] != 124 {
		t.Fatalf("native0x47A5DA ToLocker=%x", p)
	}
	for _, action := range []byte{0, 48, 255} {
		if CashActionWireReady(action) {
			t.Fatalf("unverified native action%d would mutate", action)
		}
	}
	for _, action := range []byte{3, 4, 5, 14, 15, 33, 34, 46} {
		if !CashActionWireReady(action) {
			t.Fatalf("confirmed native action%d disabled", action)
		}
	}
}

func TestCashNativeGiftAndCouponRequests(t *testing.T) {
	// Raw field order follows native 0x4736DD/0x4736F6 (action/flag),
	// 0x8D59DE (PIC), 0x473953 (SN), 0x47396C/0x473985 (two
	// strings), and 0x47398E (final option). No IDB metadata was changed.
	data, _ := hex.DecodeString("000600313435313435b19698000300426f620200686900")
	got, err := ReadNativeCashGift(4, NewReader(data))
	if err != nil || got.PIN != "145145" || got.SN != 10000049 || got.Recipient != "Bob" || got.Message != "hi" || got.Kind != "gift" {
		t.Fatalf("native gift=%+v %v", got, err)
	}
	for n := 0; n < len(data); n++ {
		if _, err = ReadNativeCashGift(4, NewReader(data[:n])); err == nil {
			t.Fatalf("truncated gift accepted%d", n)
		}
	}
	if v, err := ReadNativeCashGift(34, NewReader(data[1:])); err != nil || v.Kind != "gift_package" || v.PIN != "145145" {
		t.Fatalf("package gift=%+v %v", v, err)
	}
	if v, err := ReadNativeDirectGift(NewReader(data[1 : len(data)-1])); err != nil || v.SN != 10000049 {
		t.Fatalf("0x195 gift=%+v %v", v, err)
	}
	// 0x47479C mode0, 0x4747B5/CE two strings, 0x4747D9 option.
	coupon, _ := hex.DecodeString("00030051413104003134353100")
	if v, err := ReadNativeCashCoupon(NewReader(coupon)); err != nil || v.Coupon != "QA1:1451" {
		t.Fatalf("native coupon=%+v %v", v, err)
	}
	for n := 0; n < len(coupon); n++ {
		if _, err = ReadNativeCashCoupon(NewReader(coupon[:n])); err == nil {
			t.Fatalf("truncated coupon accepted%d", n)
		}
	}
	coupon[0] = 1
	if _, err = ReadNativeCashCoupon(NewReader(coupon)); err == nil {
		t.Fatal("unsupported coupon mode accepted")
	}
}
