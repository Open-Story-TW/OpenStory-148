package content

import "testing"

func TestCashCataloguePreservesNativeLimitModes(t *testing.T) {
	for _, tc := range []struct{ sn, mode int }{{10000049, 0}, {10003366, 2}, {10003386, 3}} {
		p, err := CashProduct(tc.sn)
		if err != nil || p.LimitMode != tc.mode {
			t.Fatalf("SN%d limit %d: %v", tc.sn, p.LimitMode, err)
		}
	}
	all, err := CashProducts()
	if err != nil {
		t.Fatal(err)
	}
	modes := map[int]int{}
	for _, p := range all {
		modes[p.LimitMode]++
	}
	if modes[2] != 13 || modes[3] != 1 {
		t.Fatalf("native WZ Limit distribution changed: %+v", modes)
	}
}
