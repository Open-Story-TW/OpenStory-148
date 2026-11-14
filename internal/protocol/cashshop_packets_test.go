package protocol

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestCashApparelLookAndFullInventory(t *testing.T) {
	c := domain.Character{Gender: 0, Face: 20100, Hair: 30000, Equipment: []domain.Equipment{
		{ItemID: 1022000, Slot: -3}, {ItemID: 1322005, Slot: -11},
		{ItemID: 1022009, Slot: -103, CashID: 5}, {ItemID: 1702000, Slot: -111, CashID: 6},
	}}
	w := NewRawWriter()
	addCharLookMode(w, c, false)
	r := NewReader(w.Packet())
	r.Skip(15) // gender, skin, face, job, mega, hair.
	if r.U8() != 3 || r.I32() != 1022009 || r.U8() != 11 || r.I32() != 1322005 {
		t.Fatalf("cash accessory must override only its normal slot: %x", w.Packet())
	}
	if r.U8() != 255 || r.U8() != 255 || r.U8() != 255 || r.I32() != 1702000 {
		t.Fatal("cash weapon must be the separate appearance field")
	}
	for _, tc := range []struct {
		source, destination int
		hex                 string
	}{
		{1, -103, "2a000101000201010099ff02"},
		{-103, 1, "2a00010100020199ff010001"},
	} {
		want, _ := hex.DecodeString(tc.hex)
		got := ModifyInventory([]domain.InventoryChange{{Mode: 2, InvType: 1, Slot: tc.source, Destination: tc.destination}})
		if !bytes.Equal(got, want) {
			t.Fatalf("cash equipment move got%x want%x", got, want)
		}
	}
	full := NewRawWriter()
	addCharacterInfo(full, c, time.Unix(0, 0))
	equip := NewRawWriter()
	equip.U16(3) // PacketHelper.getItemPosition(-103) = 3 in cash list.
	addEquipment(equip, c.Equipment[2])
	if !bytes.Contains(full.Packet(), equip.Packet()) {
		t.Fatal("full character inventory omitted worn cash accessory")
	}
}

func TestCashCatalogPacketFitsV145Frame(t *testing.T) {
	products, err := content.CashProducts()
	if err != nil {
		t.Fatal(err)
	}
	p := CashInfo("goqa01", products)
	if len(p) < 1200 || len(p) > MaxPacketSize {
		t.Fatalf("CS_INFO size=%d", len(p))
	}
	r := NewReader(p)
	if r.U16() != SendCashInfo || r.String() != "goqa01" || r.I32() != 0 {
		t.Fatal("CS_INFO identity/header")
	}
	count := int(r.U16())
	if count != 2409 {
		t.Fatalf("source on-sale count=%d", count)
	}
	for i := 0; i < count; i++ {
		if r.I32() <= 0 || r.I32() != 0x627 || r.I32() <= 0 || r.U16() == 0 || r.I32() <= 0 {
			t.Fatalf("invalid catalog record %d", i)
		}
		r.U16()
		r.U8()
		if r.U8() != 1 {
			t.Fatal("onsale flag")
		}
	}
	if r.Remaining() != 1200 || r.Err() != nil {
		t.Fatalf("sale-info fixed trailer=%d err=%v", r.Remaining(), r.Err())
	}
}

func TestCashIdentityKeepsNormalLayoutAndEncodesSerialExpiry(t *testing.T) {
	plain := NewRawWriter()
	addBundleItem(plain, domain.InventoryItem{TemplateID: 2000000, Quantity: 5})
	if len(plain.Packet()) != 24 {
		t.Fatalf("normal bundle changed length %d", len(plain.Packet()))
	}
	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	cash := NewRawWriter()
	addBundleItem(cash, domain.InventoryItem{CashID: 1234, ExpiresAt: expires.UnixMilli(), TemplateID: 2000000, Quantity: 5})
	r := NewReader(cash.Packet())
	if r.U8() != 2 || r.I32() != 2000000 || r.U8() != 1 || r.I64() != 1234 || r.I64() != mapleTime(expires) {
		t.Fatal("cash serial/expiry layout")
	}
	if len(cash.Packet()) != len(plain.Packet())+8 {
		t.Fatal("cash serial width")
	}
	item := domain.CashItem{ID: 1234, AccountID: 99, TemplateID: 2000000, SN: 80000277, Quantity: 5, ExpiresAt: expires.UnixMilli()}
	p := CashPurchased(item)
	if len(p) != 70 {
		t.Fatalf("cash item receipt width=%d", len(p))
	}
	r = NewReader(p)
	if r.U16() != SendCashOperation || r.U8() != 103 || r.I64() != 1234 || r.I64() != 99 || r.I32() != 2000000 || r.I32() != 80000277 || r.U16() != 5 {
		t.Fatal("cash purchase receipt identity")
	}
}

func TestNativeCashLockerEquipmentRecordsRemainAligned(t *testing.T) {
	items := []domain.CashItem{
		{ID: 1671, AccountID: 51, SN: 10003366, TemplateID: 1102353, Quantity: 1, InvType: 1, Equipment: &domain.Equipment{ItemID: 1102353}},
		{ID: 1672, AccountID: 51, SN: 10000049, TemplateID: 1022009, Quantity: 1, InvType: 1, Equipment: &domain.Equipment{ItemID: 1022009}},
	}
	packet := CashLocker(items)
	// Native47A205: header5 + cash-info2*67 + count4 + equip2*112 (v148) + footer8.
	if len(packet) != 375 {
		t.Fatalf("cash locker length=%d; native two-record size is375", len(packet))
	}
	r := NewReader(packet)
	if r.U16() != SendCashOperation || r.U8() != 95 || r.U16() != 2 {
		t.Fatal("locker header")
	}
	for _, item := range items {
		if r.I64() != item.ID {
			t.Fatal("cash-info identity")
		}
		r.Skip(59) // remainder of native fixed67-byte cash-info record.
	}
	if r.I32() != 2 {
		t.Fatal("equipment count")
	}
	for _, item := range items {
		if r.U8() != 1 || r.I32() != int32(item.TemplateID) || r.U8() != 1 || r.I64() != item.ID || r.I64() != maxTime {
			t.Fatal("equipment factory type/item/cash serial misaligned")
		}
		// Native4C373B consumes the equip fields through offset94, skips its
		// separate unique-ID decode when cash serial is nonzero, then reads
		// timestamp8 + trailingI32. A spurious uniqueID corrupts the next type.
		r.Skip(78) // v148: three more u16 than v145.
		if r.I64() != zeroTime || r.I32() != -1 {
			t.Fatal("cash equipment timestamp/trailer misaligned")
		}
	}
	if r.U16() != 48 || r.U16() != 15 || r.U16() != 0 || r.U16() != 4 || r.Remaining() != 0 || r.Err() != nil {
		t.Fatal("native locker footer misaligned")
	}
	plain := NewRawWriter()
	addEquipment(plain, domain.Equipment{ItemID: 1022000})
	if len(plain.Packet()) != 112 {
		t.Fatal("normal equipment wire changed")
	}
	r = NewReader(plain.Packet())
	r.Skip(92)
	if r.I64() != -1 || r.I64() != zeroTime || r.I32() != -1 || r.Remaining() != 0 {
		t.Fatal("normal equipment must retain separate unique ID")
	}
}
