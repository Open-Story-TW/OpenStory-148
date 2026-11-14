package protocol

import (
	"bytes"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestITCOpenEndsWithTheHeaderCITCReads(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	c := domain.Character{ID: 7, Name: "Seller", Level: 30}
	stage := NewWriter(SendITCOpen)
	addCharacterInfo(stage, c, now)
	head := stage.Packet()
	p := ITCOpen(c, "acct", now)
	if !bytes.Equal(p[:len(head)], head) {
		t.Fatal("ITCOpen must start with the 0xD2 opcode and the character data")
	}
	r := NewReader(p[len(head):])
	if r.String() != "acct" {
		t.Fatal("account name")
	}
	for _, want := range []int32{domain.ITCRegistrationFee, domain.ITCCommissionRate, domain.ITCCommissionBase, ITCMinHours, ITCMaxHours} {
		if got := r.I32(); got != want {
			t.Fatalf("header int %d, want %d", got, want)
		}
	}
	if r.I64() != mapleTime(now) || r.Remaining() != 0 || r.Err() != nil {
		t.Fatal("server time must close the header")
	}
}

func TestITCListRequestAndEmptyAnswer(t *testing.T) {
	// 0x559B02: tab, type, page, two sort bytes, category, keyword.
	body := []byte{2, 0, 0, 0, 1, 0, 0, 0, 3, 0, 0, 0, 1, 0, 1, 0, 0, 0, 2, 0, 'a', 'b'}
	req, err := ReadITCListRequest(NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if req != (ITCListRequest{Tab: 2, Type: 1, Page: 3, SortA: 1, SortB: 0, Category: 1, Keyword: "ab"}) {
		t.Fatalf("request %+v", req)
	}
	if _, err = ReadITCListRequest(NewReader(append(body, 0))); err == nil {
		t.Fatal("trailing bytes must be refused")
	}
	r := NewReader(ITCEmptyList(req))
	if r.U16() != SendITCNormalItemResult || r.U8() != 20 || r.I32() != 0 || r.I32() != 0 {
		t.Fatal("list result head")
	}
	if r.I32() != 2 || r.I32() != 1 || r.I32() != 3 || r.U8() != 1 || r.U8() != 0 || r.U8() != 1 || r.Remaining() != 0 {
		t.Fatal("list result must echo tab, type, page and sorts, then release the request gate")
	}
}

func TestITCSmallAnswers(t *testing.T) {
	if got := ITCQueryCash(-5); !bytes.Equal(got, []byte{0x9d, 0x02, 0, 0, 0, 0}) {
		t.Fatalf("negative balance must be sent as 0: % x", got)
	}
	if got := ITCQueryCash(1 << 40); !bytes.Equal(got[2:], []byte{0xff, 0xff, 0xff, 0x7f}) {
		t.Fatalf("balance must be clamped: % x", got)
	}
	if got := ITCChargeResult(); !bytes.Equal(got, []byte{0x9c, 0x02}) {
		t.Fatalf("charge result % x", got)
	}
	if got := ITCFailure(0); !bytes.Equal(got, []byte{0x9e, 0x02, 23, 0}) {
		t.Fatalf("failure % x", got)
	}
}

func TestITCRegisterReadsTheItemHeadAndTheFixedTail(t *testing.T) {
	// 0x558EA0 type 0: action byte already read; item blob, then slot,
	// quantity, price, 7 days and 1.
	w := NewWriter(0)
	addBundleItem(w, domain.InventoryItem{TemplateID: 2000000, Quantity: 31})
	blob := w.Packet()[2:]
	body := append([]byte{}, blob...)
	body = append(body, 3, 0, 0, 0, 5, 0, 0, 0, 0xe8, 3, 0, 0, 7, 1)
	reg, err := ReadITCRegister(NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if reg != (ITCRegisterRequest{ItemType: 2, TemplateID: 2000000, Slot: 3, Quantity: 5, Price: 1000, Days: 7}) {
		t.Fatalf("register %+v", reg)
	}
	body[len(body)-1] = 0
	if _, err = ReadITCRegister(NewReader(body)); err == nil {
		t.Fatal("the closing flag must be 1")
	}
	if _, err = ReadITCRegister(NewReader([]byte{2, 0x80, 0x84, 0x1e, 0})); err == nil {
		t.Fatal("a short body must be refused")
	}
}

func TestITCListWritesEachListingAfterTheHeader(t *testing.T) {
	ends := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	l := domain.ITCListing{ID: 9, SellerName: "Seller", Item: domain.InventoryItem{InvType: 2, TemplateID: 2000000, Quantity: 5}, Price: 1000, ListedAt: ends.Add(-time.Hour), EndsAt: ends}
	req := ITCListRequest{Tab: 1, Type: 0, Page: 0}
	r := NewReader(ITCList(req, domain.ITCPage{Total: 1, Items: []domain.ITCListing{l}}))
	if r.U16() != SendITCNormalItemResult || r.U8() != ITCResultList || r.I32() != 1 || r.I32() != 1 || r.I32() != 1 || r.I32() != 0 || r.I32() != 0 || r.U8() != 0 || r.U8() != 0 {
		t.Fatal("list header")
	}
	item := NewWriter(0)
	addBundleItem(item, l.Item)
	r.Skip(len(item.Packet()) - 2)
	if r.I32() != 9 || r.I32() != 50 || r.I32() != 1000 {
		t.Fatal("id, commission and price follow the item")
	}
	r.Skip(8)
	if r.I64() != mapleTime(ends) {
		t.Fatal("end date")
	}
	r.Skip(4)
	if r.String() != "Seller" || r.String() != "Seller" || r.String() != "Seller" {
		t.Fatal("seller names")
	}
	r.Skip(4 * 6)
	r.Skip(2 + 8 + 4)
	if r.U8() != 1 || r.Remaining() != 0 || r.Err() != nil {
		t.Fatal("the list must end with the gate byte")
	}
}

func TestITCListingID(t *testing.T) {
	if id, err := ReadITCListingID(NewReader([]byte{7, 0, 0, 0})); err != nil || id != 7 {
		t.Fatalf("id %d %v", id, err)
	}
	if _, err := ReadITCListingID(NewReader([]byte{0, 0, 0, 0})); err == nil {
		t.Fatal("id 0 must be refused")
	}
}

func TestITCAuctionAndBidRequests(t *testing.T) {
	w := NewWriter(0)
	addBundleItem(w, domain.InventoryItem{TemplateID: 2000000, Quantity: 3})
	body := append([]byte{}, w.Packet()[2:]...)
	// slot 2, quantity 3, start 100, buy-now 500, 48 hours, 1, step 10.
	body = append(body, 2, 0, 0, 0, 3, 0, 0, 0, 100, 0, 0, 0, 0xf4, 1, 0, 0, 48, 1, 10, 0, 0, 0)
	a, err := ReadITCAuction(NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if a != (ITCAuctionRequest{ItemType: 2, TemplateID: 2000000, Slot: 2, Quantity: 3, StartPrice: 100, BuyNowPrice: 500, Hours: 48, BidStep: 10}) {
		t.Fatalf("auction %+v", a)
	}
	b, err := ReadITCBid(NewReader([]byte{7, 0, 0, 0, 100, 0, 0, 0, 10, 0, 0, 0}))
	if err != nil || b != (ITCBidRequest{ListingID: 7, Base: 100, Raise: 10}) {
		t.Fatalf("bid %+v %v", b, err)
	}
	if _, err = ReadITCBid(NewReader([]byte{7, 0, 0, 0, 100, 0, 0, 0, 0, 0, 0, 0})); err == nil {
		t.Fatal("a bid must raise the price")
	}
}

func TestITCWantedFillAndSearchRequests(t *testing.T) {
	w := NewWriter(0)
	w.I32(4000000)
	w.I32(600)
	w.I32(3)
	w.U8(1)
	w.U8(1)
	w.String("殼")
	want, err := ReadITCWanted(NewReader(w.Packet()[2:]))
	if err != nil || want != (ITCWantedRequest{TemplateID: 4000000, Price: 600, Quantity: 3, Comment: "殼"}) {
		t.Fatalf("wanted %+v %v", want, err)
	}
	item := NewWriter(0)
	addBundleItem(item, domain.InventoryItem{TemplateID: 4000000, Quantity: 10})
	body := append([]byte{4, 20, 0, 0, 0}, item.Packet()[2:]...)
	body = append(body, 9, 0, 0, 0)
	fill, err := ReadITCFill(NewReader(body))
	if err != nil || fill != (ITCFillRequest{InvType: 4, Slot: 20, ListingID: 9}) {
		t.Fatalf("fill %+v %v", fill, err)
	}
	s := NewWriter(0)
	s.I32(2)
	s.I32(0)
	s.I32(0)
	s.I32(1)
	s.String("藍")
	search, err := ReadITCSearch(NewReader(s.Packet()[2:]))
	if err != nil || search != (ITCSearchRequest{Tab: 2, ByItemName: true, Keyword: "藍"}) {
		t.Fatalf("search %+v %v", search, err)
	}
	l := domain.ITCListing{ID: 3, Kind: domain.ITCWanted, Item: domain.InventoryItem{InvType: 4, TemplateID: 4000000, Quantity: 3}, Price: 600}
	r := NewReader(ITCSearchList(ITCListRequest{Tab: 2}, domain.ITCPage{Total: 1, Items: []domain.ITCListing{l}}))
	if r.U16() != SendITCNormalItemResult || r.U8() != ITCResultSearch || r.I32() != 1 || r.I32() != 1 || r.I32() != 2 || r.I32() != 0 || r.I32() != 0 {
		t.Fatal("search head")
	}
	head := NewWriter(0)
	addBundleItem(head, l.Item)
	r.Skip(len(head.Packet()) - 2)
	if r.I32() != 3 || r.I32() != 600 || r.I32() != 0 {
		t.Fatal("a buy request carries its price at +36")
	}
}
