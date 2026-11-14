package protocol

import (
	"encoding/hex"
	"testing"
)

// Native66506B..6650BC uses one price DWORD after quantity. Native6619A9
// has no price tail;6605F6 sends only recharge slot;660479 closes with mode3.
func TestNativeNPCShopRequestExactLayouts(t *testing.T) {
	cases := []struct {
		wire string
		want NPCShopRequest
	}{
		{"00040080841e00020005000000", NPCShopRequest{Mode: 0, Index: 4, TemplateID: 2000000, Quantity: 2, DisplayedUnitPrice: 5}},
		{"01190080841e000200", NPCShopRequest{Mode: 1, Slot: 25, TemplateID: 2000000, Quantity: 2}},
		{"021900", NPCShopRequest{Mode: 2, Slot: 25}},
		{"03", NPCShopRequest{Mode: 3}},
	}
	for _, c := range cases {
		wire, _ := hex.DecodeString(c.wire)
		got, err := ReadNPCShopRequest(NewReader(wire))
		if err != nil || got != c.want {
			t.Fatalf("wire%s got%+v err%v", c.wire, got, err)
		}
		for n := 0; n < len(wire); n++ {
			if _, err = ReadNPCShopRequest(NewReader(wire[:n])); err == nil {
				t.Fatalf("accepted truncated%s at%d", c.wire, n)
			}
		}
		if _, err = ReadNPCShopRequest(NewReader(append(wire, 0))); err == nil {
			t.Fatalf("accepted trailing byte%s", c.wire)
		}
	}
	// The old Java-only layout is specifically incomplete for this client.
	old, _ := hex.DecodeString("00040080841e000200")
	if _, err := ReadNPCShopRequest(NewReader(old)); err == nil {
		t.Fatal("missing native displayed price accepted")
	}
	if _, err := ReadNPCShopRequest(NewReader([]byte{4})); err == nil {
		t.Fatal("unknown mode became close")
	}
}
