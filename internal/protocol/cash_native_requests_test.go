package protocol

import "testing"

func TestNativeCashPurchaseRequiresTrailingOption(t *testing.T) {
	input := []byte{0, 0x31, 0x2d, 0x98, 0, 0}
	request, err := ReadNativeCashPurchase(NewReader(input))
	if err != nil || request.Currency != 1 || request.SN != 0x982d31 || request.Option != 0 {
		t.Fatalf("purchase request: %+v %v", request, err)
	}
	input[5] = 7
	request, err = ReadNativeCashPurchase(NewReader(input))
	if err != nil || request.Option != 7 {
		t.Fatal("purchase option was silently discarded")
	}
	for _, malformed := range [][]byte{input[:5], append(append([]byte(nil), input...), 0)} {
		if _, err := ReadNativeCashPurchase(NewReader(malformed)); err == nil {
			t.Fatal("accepted incomplete or trailing purchase request")
		}
	}
}

func TestNativeCashMoveRequiresTypeAndExactTarget(t *testing.T) {
	withdraw := []byte{9, 0, 0, 0, 0, 0, 0, 0, 2, 25, 0}
	r, err := ReadNativeCashMove(14, NewReader(withdraw))
	if err != nil || r.CashID != 9 || r.InvType != 2 || r.TargetSlot != 25 || r.ToLocker {
		t.Fatalf("withdraw: %+v %v", r, err)
	}
	r, err = ReadNativeCashMove(15, NewReader(withdraw[:9]))
	if err != nil || !r.ToLocker || r.InvType != 2 || r.TargetSlot != 0 {
		t.Fatalf("deposit: %+v %v", r, err)
	}
	for _, malformed := range [][]byte{withdraw[:8], withdraw[:9], append(append([]byte(nil), withdraw...), 0)} {
		if _, err := ReadNativeCashMove(14, NewReader(malformed)); err == nil {
			t.Fatal("accepted old/trailing withdrawal shape")
		}
	}
	withdraw[9] = 0
	if _, err := ReadNativeCashMove(14, NewReader(withdraw)); err == nil {
		t.Fatal("network withdrawal accepted unspecified slot zero")
	}
}
