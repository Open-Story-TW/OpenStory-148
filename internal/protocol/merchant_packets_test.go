package protocol

import (
	"bytes"
	"errors"
	"testing"

	"maple-go-server/internal/domain"
)

func merchantFixture() domain.MerchantShop {
	return domain.MerchantShop{
		ID: 7, CharacterID: 0x01020304, AccountID: 9, ChannelID: 2, MapID: 910000001, X: -120, Y: 34, Foothold: 5,
		PermitItemID: 5030001, OwnerName: "abc", Title: "shop", State: domain.MerchantOpen, Mesos: 5_000_000_000,
		Items: []domain.MerchantItem{{ID: 1, Position: 1, Bundles: 3, PerBundle: 10, Price: 1000, Item: domain.InventoryItem{InvType: 2, TemplateID: 2000000, Quantity: 10}}},
		Sales: []domain.MerchantSale{{TemplateID: 2000000, Bundles: 2, PerBundle: 10, Total: 2000, Buyer: "bob"}},
	}
}

// The employee spawn is decoded by 0x4EFEC6 (owner, permit), 0x4EF265
// (x, y, foothold, owner name) and 0x4EE89C (balloon: type, serial, title and
// three bytes).
func TestEmployeeEnterLayout(t *testing.T) {
	got := EmployeeEnter(merchantFixture(), 0x40000001, 2)
	want := []byte{
		0xdd, 0x01,
		0x04, 0x03, 0x02, 0x01, // owner
		0x71, 0xc0, 0x4c, 0x00, // 5030001
		0x88, 0xff, 0x22, 0x00, 0x05, 0x00, // x, y, foothold
		0x03, 0x00, 'a', 'b', 'c',
		0x05,                   // entrusted shop
		0x01, 0x00, 0x00, 0x40, // serial
		0x04, 0x00, 's', 'h', 'o', 'p',
		0x01, 0x02, 0x06, // stand, visitors, seats
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("employee enter\n got % x\nwant % x", got, want)
	}
	balloon := EmployeeBalloon(merchantFixture(), 0x40000001, 2)
	if !bytes.Equal(balloon[:2], []byte{0xe0, 0x01}) || !bytes.Equal(balloon[6:], want[21:]) {
		t.Fatalf("balloon % x", balloon)
	}
	if got := EmployeeLeave(0x01020304); !bytes.Equal(got, []byte{0xde, 0x01, 4, 3, 2, 1}) {
		t.Fatalf("employee leave % x", got)
	}
}

// 0x5F3779 reads type, seats, own seat, then seat 0 as the employee (permit
// id + name, no job). 0x4F5935 gives only the owner the sold list and the
// 8-byte income; everyone gets the 32-bit display income before the items.
func TestMerchantRoomOwnerAndVisitor(t *testing.T) {
	defer domain.SetLongMoneyForTest(false)()
	shop := merchantFixture()
	owner := MerchantRoom(shop, 0, nil, nil, true, 60)
	head := []byte{0x4b, 0x02, 10, 5, 6, 0, 0, 0x71, 0xc0, 0x4c, 0x00}
	if !bytes.HasPrefix(owner, head) {
		t.Fatalf("owner head % x", owner[:len(head)])
	}
	income := []byte{0x00, 0xf2, 0x05, 0x2a, 0x01, 0x00, 0x00, 0x00} // 5,000,000,000
	if !bytes.Contains(owner, income) {
		t.Fatal("owner dialog must carry the 64-bit income")
	}
	visitor := MerchantRoom(shop, 1, []MerchantVisitor{{Seat: 1, Character: domain.Character{Name: "bob", Job: 100}}}, []MerchantChatLine{{Text: "abc : hi", Seat: 0}}, false, 0)
	if visitor[5] != 1 {
		t.Fatalf("visitor seat %d", visitor[5])
	}
	if bytes.Contains(visitor, income) {
		t.Fatal("a visitor must not receive the owner's income")
	}
	// The display income is clamped to int32 for both.
	clamp := []byte{0xff, 0xff, 0xff, 0x7f, 1, 3, 0, 10, 0, 0xe8, 0x03, 0x00, 0x00}
	if !bytes.Contains(owner, clamp) || !bytes.Contains(visitor, clamp) {
		t.Fatal("item list must start with clamped income, count, bundles, per bundle, price")
	}
	if !bytes.HasSuffix(owner, []byte{0, 0}) {
		t.Fatal("item list ends with the empty second list and flag")
	}
}

func TestStoreBankOpenGroupsItemsByInventory(t *testing.T) {
	defer domain.SetLongMoneyForTest(false)()
	shop := merchantFixture()
	shop.Mesos = 1234
	shop.Items = append(shop.Items, domain.MerchantItem{ID: 2, Bundles: 0, PerBundle: 1, Price: 5, Item: domain.InventoryItem{InvType: 4, TemplateID: 4000000}})
	got := StoreBankOpen(9030000, shop)
	head := []byte{0x48, 0x02, 38, 0x70, 0xc9, 0x89, 0x00, 16, 0x7e, 0, 0, 0, 0, 0, 0, 0, 0xd2, 0x04, 0, 0, 0 /* equips */, 1 /* use */}
	if !bytes.HasPrefix(got, head) {
		t.Fatalf("store bank head % x", got[:len(head)])
	}
	// One USE stack of 30 (3 lots of 10), then empty SETUP, ETC (sold out) and CASH.
	if !bytes.HasSuffix(got, []byte{0, 0, 0}) {
		t.Fatalf("store bank tail % x", got[len(got)-4:])
	}
	if !bytes.Contains(got, []byte{0x1e, 0x00, 0x00, 0x00, 0x00, 0x00}) {
		t.Fatal("stack quantity must be bundles x per bundle")
	}
}

func TestReadMerchantAction(t *testing.T) {
	defer domain.SetLongMoneyForTest(false)()
	create := NewRawWriter()
	create.U8(6)
	create.U8(5)
	create.String("my shop")
	create.U8(0)
	create.I16(3)
	create.I32(5030000)
	action, roomType, _ := PeekMiniRoom(create.Packet())
	if action != 6 || roomType != 5 {
		t.Fatalf("peek create %d %d", action, roomType)
	}
	got, err := ReadMerchantAction(NewReader(create.Packet()))
	if err != nil || got.Title != "my shop" || got.Slot != 3 || got.ItemID != 5030000 || got.RoomType != 5 {
		t.Fatalf("create %+v %v", got, err)
	}
	enter := []byte{9, 0x02, 0x00, 0x00, 0x40, 0, 0}
	if action, _, serial := PeekMiniRoom(enter); action != 9 || serial != 0x40000002 || serial <= MerchantRoomBase {
		t.Fatalf("peek enter %d %#x", action, serial)
	}
	// A trade accept shares action 9 but its reference stays below the base.
	if _, _, serial := PeekMiniRoom([]byte{9, 5, 0, 0, 0, 0, 0}); serial > MerchantRoomBase {
		t.Fatal("trade reference mistaken for a merchant serial")
	}
	put := []byte{21, 2, 4, 0, 3, 0, 10, 0, 0xe8, 0x03, 0, 0}
	got, err = ReadMerchantAction(NewReader(put))
	if err != nil || got.InvType != 2 || got.Slot != 4 || got.Bundles != 3 || got.PerBundle != 10 || got.Price != 1000 {
		t.Fatalf("put %+v %v", got, err)
	}
	buy := []byte{22, 1, 2, 0, 0xaa, 0xbb, 0xcc, 0xdd}
	got, err = ReadMerchantAction(NewReader(buy))
	if err != nil || got.Index != 1 || got.Bundles != 2 {
		t.Fatalf("buy %+v %v", got, err)
	}
	take := []byte{28, 0, 2, 0}
	got, err = ReadMerchantAction(NewReader(take))
	if err != nil || got.Index != 2 {
		t.Fatalf("take %+v %v", got, err)
	}
	if _, err = ReadMerchantAction(NewReader([]byte{21, 2, 4})); !errors.Is(err, ErrMalformed) {
		t.Fatalf("short put: %v", err)
	}
	if _, err = ReadMerchantAction(NewReader([]byte{70, 1})); !errors.Is(err, ErrUnsupportedTradeAction) {
		t.Fatalf("mini game action: %v", err)
	}
}

// Under the long money contract every merchant meso field is 8 bytes:
// the put price the client sends, each row price, the display income, the
// sale totals and the store bank amount.
func TestMerchantLongMoneyLayout(t *testing.T) {
	defer domain.SetLongMoneyForTest(true)()
	price := int64(5_130_042_002_123) // 5兆1300億4200萬2123
	wide := []byte{0xcb, 0xca, 0x54, 0x6e, 0xaa, 0x04, 0x00, 0x00}
	put := append([]byte{21, 2, 4, 0, 3, 0, 10, 0}, wide...)
	got, err := ReadMerchantAction(NewReader(put))
	if err != nil || got.Price != price {
		t.Fatalf("wide put %+v %v", got, err)
	}
	if _, err = ReadMerchantAction(NewReader(put[:12])); !errors.Is(err, ErrMalformed) {
		t.Fatalf("a 4-byte price under long money: %v", err)
	}
	shop := merchantFixture()
	shop.Items[0].Price = price
	shop.Sales[0].Total = price
	income := []byte{0x00, 0xf2, 0x05, 0x2a, 0x01, 0x00, 0x00, 0x00} // 5,000,000,000
	list := MerchantItems(shop)
	want := append(append([]byte{0x4b, 0x02, 46}, income...), 1, 3, 0, 10, 0)
	want = append(want, wide...)
	if !bytes.HasPrefix(list, want) {
		t.Fatalf("wide list\n got % x\nwant % x", list[:len(want)], want)
	}
	owner := MerchantRoom(shop, 0, nil, nil, false, 0)
	sold := append(append([]byte{0x80, 0x84, 0x1e, 0x00, 20, 0}, wide...), 3, 0, 'b', 'o', 'b')
	if !bytes.Contains(owner, sold) {
		t.Fatal("the sold list total must be 8 bytes")
	}
	if got := MerchantArranged(price); !bytes.Equal(got, append([]byte{0x4b, 2, 30}, wide...)) {
		t.Fatalf("arranged % x", got)
	}
	bank := StoreBankOpen(9030000, shop)
	head := append([]byte{0x48, 0x02, 38, 0x70, 0xc9, 0x89, 0x00, 16, 0x7e, 0, 0, 0, 0, 0, 0, 0}, income...)
	if !bytes.HasPrefix(bank, head) {
		t.Fatalf("wide store bank % x", bank[:len(head)])
	}
	if features := ClientConfigFeatures(); features&ClientFeatureLongMerchant == 0 || features&ClientFeatureLongMoney == 0 {
		t.Fatal("the launcher must be told the merchant layout is wide")
	}
}

func TestMerchantSmallPackets(t *testing.T) {
	defer domain.SetLongMoneyForTest(false)()
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"check", MerchantCheck(MerchantCheckOpenTitle), []byte{0x41, 0, 7}},
		{"open at", MerchantCheckOpenAt(910000003, 2), []byte{0x41, 0, 8, 0x83, 0x7f, 0x3d, 0x36, 1}},
		{"chat", MerchantChat("a", 2), []byte{0x4b, 2, 14, 13, 2, 1, 0, 'a'}},
		{"left", MerchantVisitorLeft(3), []byte{0x4b, 2, 18, 3}},
		{"expelled", MerchantExpelled(3, MerchantLeaveMaintenance), []byte{0x4b, 2, 18, 3, 18}},
		{"buy", MerchantBuyResult(MerchantBuyNoMesos), []byte{0x4b, 2, 43, 2}},
		{"sold", MerchantSold(1, 2, "b"), []byte{0x4b, 2, 47, 1, 2, 0, 1, 0, 'b'}},
		{"arranged", MerchantArranged(5_000_000_000), []byte{0x4b, 2, 30, 0xff, 0xff, 0xff, 0x7f}},
		{"withdraw", MerchantWithdrawResult(MerchantWithdrawNoSpace), []byte{0x4b, 2, 32, 3}},
		{"enter failed", MerchantEnterFailed(MiniRoomMaintenance), []byte{0x4b, 2, 10, 0, 18, 0, 0}},
		{"bank empty", StoreBankEmpty(), []byte{0x48, 2, 40, 0xff, 0xc9, 0x9a, 0x3b, 0xff, 0xc9, 0x9a, 0x3b, 0}},
		{"bank result", StoreBankResult(StoreBankTaken), []byte{0x47, 2, 32}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s\n got % x\nwant % x", c.name, c.got, c.want)
		}
	}
}

// 0x617: version, then the merchant and trade tables, each a count and
// {from i64, rate u32 in parts per 100,000}.
func TestClientFeesPacketLayout(t *testing.T) {
	defer domain.SetMerchantFees(domain.MerchantFees())
	defer domain.SetTradeFees(domain.TradeFees())
	domain.SetMerchantFees([]domain.FeeTier{{From: 100000, Rate: 400}, {From: 5_000_000_000, Rate: 3000}})
	domain.SetTradeFees(nil)
	want := []byte{0x17, 0x06, 1,
		2,
		0xa0, 0x86, 0x01, 0, 0, 0, 0, 0, 0x90, 0x01, 0, 0,
		0x00, 0xf2, 0x05, 0x2a, 0x01, 0, 0, 0, 0xb8, 0x0b, 0, 0,
		0}
	if got := ClientFeesPacket(); !bytes.Equal(got, want) {
		t.Fatalf("fees packet\n got % x\nwant % x", got, want)
	}
}
