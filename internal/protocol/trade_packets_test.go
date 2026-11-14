package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

func TestTradeWireConstants(t *testing.T) {
	// The golden bytes are the stock four-byte amount layout.
	defer domain.SetLongMoneyForTest(false)()
	for _, tc := range []struct {
		got []byte
		hex string
	}{{TradeInvite("A", 17), "4b020b0301004111000000"}, {TradeMesos(1, 100), "4b02010164000000"}, {TradeConfirm(), "4b0202"}, {TradeClose(1, 7), "4b02120107"}} {
		want, _ := hex.DecodeString(tc.hex)
		if !bytes.Equal(tc.got, want) {
			t.Fatalf("got%x want%x", tc.got, want)
		}
	}
}

func TestNativeTradeRequestsConsumeExactOriginalPayloads(t *testing.T) {
	// The golden bytes are the stock four-byte amount layout.
	defer domain.SetLongMoneyForTest(false)()
	for _, fixture := range []string{
		"060300",               //4F2161
		"0b02000000",           //4F2161 invite actual CID
		"09110000000000",       //5CAEAF accept reference17 plus two reserved zeros
		"0c1100000003",         //4EAB14 decline
		"00020100050001",       //6EEDEF item type2,slot1,quantity5,tradeSlot1
		"00050200010001",       //v148 4D9DC3 cash tab item: type5,slot2,quantity1,tradeSlot1
		"0164000000",           //6EE923 positive mesos
		"020140ab1e0012345678", //6EEFC3 actual item2009920 + opaque item-info check
		"0500",                 //6EEB18 empty remote item-info acknowledgement
		"12",                   //6EE80C cancel
	} {
		data, _ := hex.DecodeString(fixture)
		if _, err := ReadTradeRequest(NewReader(data)); err != nil {
			t.Fatalf("native fixture%x:%v", data, err)
		}
		for n := 0; n < len(data); n++ {
			if _, err := ReadTradeRequest(NewReader(data[:n])); err == nil {
				t.Fatalf("accepted truncation%x", data[:n])
			}
		}
		extra := append(append([]byte(nil), data...), 0)
		if _, err := ReadTradeRequest(NewReader(extra)); err == nil {
			t.Fatalf("accepted extra tail%x", extra)
		}
	}
	for _, fixture := range []string{"0603", "060301", "09", "09000000000000", "09110000000001", "0c1100000000", "020a", "0200ff", "00060200010001", "00000200010001", "0100000000", "01ffffffff"} {
		data, _ := hex.DecodeString(fixture)
		if _, err := ReadTradeRequest(NewReader(data)); err == nil {
			t.Fatalf("accepted unsupported/malformed request%x", data)
		}
	}
}
