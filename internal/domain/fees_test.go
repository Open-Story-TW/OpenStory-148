package domain

import (
	"math"
	"testing"
)

func TestDefaultFeesMatchLegacy(t *testing.T) {
	merchant := map[int64]int64{99_999: 0, 100_000: 400, 999_999: 4_000, 1_000_000: 9_000, 5_000_000: 75_000, 10_000_000: 200_000, 25_000_000: 625_000, 100_000_000: 3_000_000, 12_000_000_000: 360_000_000}
	for amount, want := range merchant {
		if got := MerchantFee(amount); got != want {
			t.Errorf("MerchantFee(%d) = %d, want %d", amount, got, want)
		}
	}
	trade := map[int64]int64{99_999: 0, 100_000: 800, 1_000_000: 18_000, 5_000_000: 150_000, 10_000_000: 400_000, 25_000_000: 1_250_000, 100_000_000: 6_000_000}
	for amount, want := range trade {
		if got := TradeFee(amount); got != want {
			t.Errorf("TradeFee(%d) = %d, want %d", amount, got, want)
		}
	}
	if fee := MerchantFee(math.MaxInt64); fee <= 0 || fee > math.MaxInt64/30 {
		t.Fatalf("fee on the largest amount: %d", fee)
	}
}

func TestParseFeeTiers(t *testing.T) {
	tiers, err := ParseFeeTiers(" 100000:0.4 , 1000000:0.9%,5000000:12.345 ")
	if err != nil || len(tiers) != 3 || tiers[0] != (FeeTier{100000, 400}) || tiers[1] != (FeeTier{1000000, 900}) || tiers[2] != (FeeTier{5000000, 12345}) {
		t.Fatalf("parse: %+v %v", tiers, err)
	}
	if got := FormatFeeTiers(tiers); got != "100000:0.4,1000000:0.9,5000000:12.345" {
		t.Fatalf("format: %q", got)
	}
	if FormatFeeTiers(mustFees(DefaultMerchantFees)) != DefaultMerchantFees || FormatFeeTiers(mustFees(DefaultTradeFees)) != DefaultTradeFees {
		t.Fatal("the defaults are not in canonical form")
	}
	if none, err := ParseFeeTiers(""); err != nil || len(none) != 0 || Fee(none, 1_000_000_000) != 0 {
		t.Fatal("an empty table must charge nothing")
	}
	for _, bad := range []string{"100000", "abc:1", "100000:101", "100000:-1", "100000:0.1234", "200000:1,100000:2", "100000:1,100000:2", "-5:1", "1:1,2:1,3:1,4:1,5:1,6:1,7:1"} {
		if _, err := ParseFeeTiers(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if Fee([]FeeTier{{0, FeeRateScale}}, 12345) != 12345 {
		t.Fatal("a 100% fee takes the whole amount, never more")
	}
	if Fee([]FeeTier{{0, 50}}, 999) != 0 || Fee([]FeeTier{{0, 50}}, 1000) != 1 {
		t.Fatal("fees round half up")
	}
}

func TestFeeTablesAreSwappedLive(t *testing.T) {
	defer SetMerchantFees(MerchantFees())
	defer SetTradeFees(TradeFees())
	SetMerchantFees([]FeeTier{{0, 10000}})
	SetTradeFees(nil)
	if MerchantFee(1000) != 100 || TradeFee(100_000_000) != 0 {
		t.Fatalf("custom tables not applied: %d %d", MerchantFee(1000), TradeFee(100_000_000))
	}
}
