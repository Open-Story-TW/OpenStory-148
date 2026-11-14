package persistence

import "testing"

func TestTradeTaxBoundaries(t *testing.T) {
	for _, tc := range []struct{ amount, tax int64 }{{0, 0}, {99999, 0}, {100000, 800}, {999999, 8000}, {1000000, 18000}, {5000000, 150000}, {10000000, 400000}, {25000000, 1250000}, {100000000, 6000000}, {2147483647, 128849019}} {
		if got := TradeTax(tc.amount); got != tc.tax {
			t.Fatalf("tax(%d)=%d want%d", tc.amount, got, tc.tax)
		}
	}
}
