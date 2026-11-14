package domain

import (
	"math"
	"testing"
)

func TestMoneyUnits(t *testing.T) {
	for amount, want := range map[int64]string{
		0:                 "0",
		9999:              "9999",
		10000:             "1萬",
		42002123:          "4200萬2123",
		100000000:         "1億",
		2147483647:        "21億4748萬3647",
		5_130_042_002_123: "5兆1300億4200萬2123",
		1_000_000_000_005: "1兆5",
		math.MaxInt64:     "922京3372兆368億5477萬5807",
		-15000:            "-1萬5000",
	} {
		if got := MoneyUnits(amount); got != want {
			t.Errorf("MoneyUnits(%d) = %q, want %q", amount, got, want)
		}
	}
}

func TestMerchantTotalNeverWraps(t *testing.T) {
	if total, ok := MerchantTotal(3_000_000_000, 3); !ok || total != 9_000_000_000 {
		t.Fatalf("total %d %v", total, ok)
	}
	if _, ok := MerchantTotal(math.MaxInt64/2+1, 2); ok {
		t.Fatal("a total above int64 was accepted")
	}
	if total, ok := MerchantTotal(math.MaxInt64, 1); !ok || total != math.MaxInt64 {
		t.Fatal("the largest price for one lot was refused")
	}
	if _, ok := MerchantTotal(0, 1); ok {
		t.Fatal("a zero price was accepted")
	}
	defer SetLongMoneyForTest(false)()
	if MerchantMaxPrice() != math.MaxInt32 {
		t.Fatal("without long money a price is bounded by the native 32-bit field")
	}
	SetLongMoneyForTest(true)
	if MerchantMaxPrice() != math.MaxInt64 {
		t.Fatal("under long money a price may use 64 bits")
	}
}
