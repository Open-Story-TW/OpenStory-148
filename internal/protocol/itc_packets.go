package protocol

import (
	"encoding/binary"
	"time"

	"maple-go-server/internal/domain"
)

// The item trading center (拍賣, CITC in the client) as the v148 client
// speaks it. The status bar's 拍賣 button (CUIStatusBar::OnButton 0x86433A,
// id 0x3E9) calls 0x9B1BEF, which sends RecvEnterITC with no body and
// locks the client's request gate until the server answers.
const (
	RecvEnterITC uint16 = 0x14d
	// CITC status buttons (0x5635D2): 1000 charges cash, 1001 asks for the
	// balance, 1002 leaves with RecvChangeMap like the cash shop.
	RecvITCCharge    uint16 = 0x1da
	RecvITCQueryCash uint16 = 0x1db
	// RecvITCOperation carries a one-byte action; 0x558EA0..0x55A2B3 build them.
	RecvITCOperation uint16 = 0x1dc

	// SendITCOpen is CStage 0xD2 (0x6EA879): character data, then the
	// header CITC reads in 0x55A0C7.
	SendITCOpen uint16 = 0xd2
	// CITC::OnPacket (0x55BF6A).
	SendITCChargeResult     uint16 = 0x29c
	SendITCQueryCashResult  uint16 = 0x29d
	SendITCNormalItemResult uint16 = 0x29e
)

// ITC actions read from RecvITCOperation. Rows of the 販售 list carry two
// buttons (0x565A53): buy (0x0F, after 您確定要購買嗎) and add to cart (8).
const (
	ITCActionRegister   byte = 0x01 // 0x558EA0 type 0: item, slot, quantity, price, days, 1
	ITCActionList       byte = 0x04 // tab, type, page, two sort bytes, category, keyword
	ITCActionCancel     byte = 0x06 // 0x5594D9 取消販售: listing id
	ITCActionTake       byte = 0x07 // 0x559575 拍賣倉庫 → inventory: listing id
	ITCActionCartAdd    byte = 0x08 // 0x55920B: listing id
	ITCActionCartRemove byte = 0x09 // 0x559308: listing id
	ITCActionBuy        byte = 0x0f // 0x559137: listing id
	ITCActionBuyCart    byte = 0x10 // 0x559275 (from 購物車): listing id
	ITCActionAuction    byte = 0x11 // 0x558EA0 type 1: item, slot, quantity, start, buy-now, hours, 1, step
	ITCActionBid        byte = 0x12 // 0x56906A: listing id, current price, raise
	ITCActionBuyNow     byte = 0x13 // 0x5591A1: listing id (直接購買 on 競標)
	ITCActionFill       byte = 0x02 // 0x5590AF: inventory type, slot, item, request id (賣給購買登記)
	ITCActionWanted     byte = 0x03 // 0x559BD1: template, price, quantity, two bytes, comment (購買登記)
	ITCActionSearch     byte = 0x05 // 0x5647AD: tab, type, 0, by item name, keyword
	ITCActionMyLeft     byte = 0x0a // 0x559372: 我的拍賣 rows past 購物車, left button
	ITCActionMyRight    byte = 0x0c // 0x55946F: the same rows, right button
)

// Normal item result types (0x55BD02 switches on type-20). The names are the
// client's own messages, decoded from its string pool.
const (
	ITCResultList          byte = 20 // 0x55B569: one page of a tab
	itcResultFailure       byte = 23 // 0x55B035: message for a code
	ITCResultRegistered    byte = 26 // 販售登記成功！
	ITCResultRegisterFail  byte = 27 // code; 69 carries a limit
	ITCResultStorage       byte = 30 // 0x55B9B3: 拍賣倉庫
	ITCResultSelling       byte = 32 // 0x55B759: 等待販售
	ITCResultCancelled     byte = 34 // 販售取消成功！
	ITCResultCancelFail    byte = 35
	ITCResultTaken         byte = 36 // 道具已移轉。 inventory type, slot
	ITCResultTakeFail      byte = 37 // code 62 also leaves the stage
	ITCResultCartAdded     byte = 38 // 放入購物車成功！
	ITCResultCartDuplicate byte = 39 // 無法重覆放入購物車！
	ITCResultCartRemoved   byte = 40 // 已從購物車列表中刪除。
	ITCResultCartFail      byte = 41 // 購物車列表刪除失敗！
	ITCResultBought        byte = 48 // 道具購買成功！
	ITCResultBuyFail       byte = 49 // code: 63 楓葉點數不足, 77 已被取消或賣出
	ITCResultAuctioned     byte = 28 // 販售申請成功！
	ITCResultAuctionFail   byte = 29 // 77 此交易已被取消, else 販售申請失敗！
	ITCResultBoughtNow     byte = 50 // 道具購買成功！
	ITCResultBuyNowFail    byte = 51 // code as for 49
	ITCResultBidFail       byte = 57 // 63 楓葉點數不足, 77 已被取消, else 無法連續下標
	ITCResultSearch        byte = 22 // 0x55B684: a search page (no sort bytes)
	ITCResultAccepted      byte = 44 // 購買申請成功！
	ITCResultAcceptFail    byte = 45 // 購買申請失敗！
	ITCResultWithdrawn     byte = 46 // 購買申請已取消。
	ITCResultWithdrawFail  byte = 47 // 購買申請取消失敗！
	ITCResultWanted        byte = 52 // 已登記到[購買]。
	ITCResultWantedFail    byte = 53 // 64 楓幣不足, 63 楓點不足, else 登記到[購買]失敗！
)

// Failure codes the client names (0x55AF1C and the buy handlers).
const (
	ITCCodeUnknown    byte = 0  // 發生不明錯誤
	ITCCodePoints     byte = 63 // 楓點不足 / 楓葉點數不足
	ITCCodeMesos      byte = 64 // 楓幣不足
	ITCCodeNoSpace    byte = 68 // 道具欄內無充足的空間
	ITCCodeLimit      byte = 69 // 超出數量上限！需在%d個以內
	ITCCodeNotAllowed byte = 76 // 該道具無法登記
	ITCCodeGone       byte = 77 // 此交易已被取消或者道具已被賣出
	ITCCodeHasBids    byte = 75 // 已有1件以上的出價紀錄！因此無法取消販售登記！
	ITCCodeRepeatBid  byte = 1  // any other code: 無法連續下標 on a bid
)

// ITCMinHours and ITCMaxHours bound an auction's length (6986).
const (
	ITCMinHours = 24
	ITCMaxHours = 168
)

// ITCOpen moves the client into the item trading center. The ints are the
// registration fee in mesos, the buyer's commission percent and flat part,
// and the auction hour range; the client adds price*rate/100+flat to every
// price it shows a buyer (0x565A53).
func ITCOpen(c domain.Character, account string, now time.Time) []byte {
	w := NewWriter(SendITCOpen)
	addCharacterInfo(w, c, now)
	w.String(account)
	w.I32(domain.ITCRegistrationFee)
	w.I32(domain.ITCCommissionRate)
	w.I32(domain.ITCCommissionBase)
	w.I32(ITCMinHours)
	w.I32(ITCMaxHours)
	w.I64(mapleTime(now))
	return w.Packet()
}

// ITCQueryCash answers the balance request (0x55ADEF). A negative balance
// makes the client show an error and leave, so it is clamped.
func ITCQueryCash(balance int64) []byte {
	w := NewWriter(SendITCQueryCashResult)
	w.I32(int32(max(0, min(balance, 2147483647))))
	return w.Packet()
}

// ITCChargeResult answers the charge button (0x55ADA0): the client releases
// its request gate and shows its own notice. This server sells no cash.
func ITCChargeResult() []byte {
	return NewWriter(SendITCChargeResult).Packet()
}

// ITCListRequest is action 4 (0x559A23, 0x559B02, 0x55A2B3).
type ITCListRequest struct {
	Tab, Type, Page int32
	SortA, SortB    byte
	Category        int32
	Keyword         string
}

// ReadITCListRequest reads action 4 after its action byte.
func ReadITCListRequest(r *Reader) (ITCListRequest, error) {
	var v ITCListRequest
	v.Tab = r.I32()
	v.Type = r.I32()
	v.Page = r.I32()
	v.SortA = r.U8()
	v.SortB = r.U8()
	v.Category = r.I32()
	v.Keyword = r.String()
	if err := r.Err(); err != nil {
		return v, err
	}
	if r.Remaining() != 0 || len(v.Keyword) > 64 || v.Page < 0 {
		return v, ErrMalformed
	}
	return v, nil
}

// ITCRegisterRequest is action 1: the client encodes the whole item it
// shows (GW_ItemSlotBase, 0x4CE29C), then fourteen fixed bytes. Only the
// item's type and template are read from the blob; the server takes the
// item from its own inventory at the slot.
type ITCRegisterRequest struct {
	ItemType   byte
	TemplateID int
	Slot       int
	Quantity   int
	Price      int64
	Days       byte
}

const itcRegisterTail = 4 + 4 + 4 + 1 + 1

func ReadITCRegister(r *Reader) (ITCRegisterRequest, error) {
	var v ITCRegisterRequest
	n := r.Remaining()
	if n < 1+4+itcRegisterTail || n > 4096 {
		return v, ErrMalformed
	}
	body := r.Bytes(n)
	if err := r.Err(); err != nil {
		return v, err
	}
	v.ItemType = body[0]
	v.TemplateID = int(int32(binary.LittleEndian.Uint32(body[1:5])))
	tail := body[n-itcRegisterTail:]
	v.Slot = int(int32(binary.LittleEndian.Uint32(tail[0:4])))
	v.Quantity = int(int32(binary.LittleEndian.Uint32(tail[4:8])))
	v.Price = int64(int32(binary.LittleEndian.Uint32(tail[8:12])))
	v.Days = tail[12]
	if v.ItemType < 1 || v.ItemType > 2 || v.TemplateID <= 0 || tail[13] != 1 {
		return v, ErrMalformed
	}
	return v, nil
}

// ReadITCListingID reads the listing id the row actions carry.
func ReadITCListingID(r *Reader) (int64, error) {
	id := r.I32()
	if err := r.Err(); err != nil {
		return 0, err
	}
	if r.Remaining() != 0 || id <= 0 {
		return 0, ErrMalformed
	}
	return int64(id), nil
}

// addITCItem writes one ITCITEM as 0x55A993 decodes it.
func addITCItem(w *Writer, l domain.ITCListing) {
	auction := l.Kind == domain.ITCAuction
	if l.Kind == domain.ITCWanted {
		// A buy request has no item of its own: the template and count.
		if l.Item.InvType == 1 {
			addEquipment(w, domain.Equipment{ItemID: l.Item.TemplateID})
		} else {
			addBundleItem(w, l.Item)
		}
		auction = false
	} else if l.Equipment != nil {
		addEquipment(w, *l.Equipment)
	} else {
		addBundleItem(w, l.Item)
	}
	w.I32(int32(l.ID))
	// The price column shows +36 plus +256 on 販售 (commission plus price,
	// what the buyer pays) and +16 plus +256 on 競標, so an auction leaves
	// both at 0 and its current bid in +16.
	if auction {
		w.I32(0)
		w.I32(0)
	} else if l.Kind == domain.ITCWanted {
		// The 購買 rows show +36 alone; 等待購買 adds +256.
		w.I32(int32(l.Price))
		w.I32(0)
	} else if l.Kind == domain.ITCOffer {
		// 等待購買 rows show +36 plus +256: the agreed price.
		w.I32(0)
		w.I32(int32(l.Price))
	} else if l.Kind == domain.ITCHistory {
		// 交易紀錄 rows show +36 alone.
		w.I32(int32(l.Price))
		w.I32(0)
	} else {
		w.I32(int32(l.Commission())) // +36
		w.I32(int32(l.Price))        // +256
	}
	w.I32(0)
	w.I32(0)
	w.I64(mapleTime(l.EndsAt)) // 結束日
	w.I32(0)
	w.String(l.SellerName) // +56: compared with the reader's own name
	w.String(l.SellerName)
	w.String(l.SellerName)
	if auction {
		// The bid dialog (0x5677CE) starts from +16 and raises by +14.
		w.I32(int32(l.BidCount))     // +12 出價次數
		w.I32(int32(l.BidStep))      // +14 出價增額
		w.I32(int32(l.CurrentPrice)) // +16 目前下標價
		w.I32(0)
		w.I32(int32(l.BuyNowPrice)) // +268 直接購買價
	} else {
		w.I32(0)
		w.I32(0)
		w.I32(0) // +16: 0 keeps the auction lines out of the tooltip
		w.I32(0)
		w.I32(0)
	}
	w.I32(0)
	w.U16(uint16(l.Status)) // +28 交易狀態 (0x565287, 0x5652D7)
	w.I64(mapleTime(l.ListedAt))
	w.I32(0)
}

// ITCAuctionRequest is action 0x11 (0x558EA0 type 1, fields from 0x5608C6).
type ITCAuctionRequest struct {
	ItemType    byte
	TemplateID  int
	Slot        int
	Quantity    int
	StartPrice  int64
	BuyNowPrice int64
	Hours       int
	BidStep     int
}

const itcAuctionTail = 4 + 4 + 4 + 4 + 1 + 1 + 4

func ReadITCAuction(r *Reader) (ITCAuctionRequest, error) {
	var v ITCAuctionRequest
	n := r.Remaining()
	if n < 1+4+itcAuctionTail || n > 4096 {
		return v, ErrMalformed
	}
	body := r.Bytes(n)
	if err := r.Err(); err != nil {
		return v, err
	}
	v.ItemType = body[0]
	v.TemplateID = int(int32(binary.LittleEndian.Uint32(body[1:5])))
	tail := body[n-itcAuctionTail:]
	v.Slot = int(int32(binary.LittleEndian.Uint32(tail[0:4])))
	v.Quantity = int(int32(binary.LittleEndian.Uint32(tail[4:8])))
	v.StartPrice = int64(int32(binary.LittleEndian.Uint32(tail[8:12])))
	v.BuyNowPrice = int64(int32(binary.LittleEndian.Uint32(tail[12:16])))
	v.Hours = int(tail[16])
	v.BidStep = int(int32(binary.LittleEndian.Uint32(tail[18:22])))
	if v.ItemType < 1 || v.ItemType > 2 || v.TemplateID <= 0 || tail[17] != 1 {
		return v, ErrMalformed
	}
	return v, nil
}

// ITCBidRequest is action 0x12: the dialog sends the price it started from
// and the raise; the bid is their sum.
type ITCBidRequest struct {
	ListingID int64
	Base      int64
	Raise     int64
}

func ReadITCBid(r *Reader) (ITCBidRequest, error) {
	var v ITCBidRequest
	v.ListingID = int64(r.I32())
	v.Base = int64(r.I32())
	v.Raise = int64(r.I32())
	if err := r.Err(); err != nil {
		return v, err
	}
	if r.Remaining() != 0 || v.ListingID <= 0 || v.Base < 0 || v.Raise < 1 {
		return v, ErrMalformed
	}
	return v, nil
}

// ITCList answers a list request; the last byte releases the request gate.
func ITCList(req ITCListRequest, page domain.ITCPage) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(ITCResultList)
	w.I32(int32(page.Total))
	w.I32(int32(len(page.Items)))
	w.I32(req.Tab)
	w.I32(req.Type)
	w.I32(req.Page)
	w.U8(req.SortA)
	w.U8(req.SortB)
	for _, l := range page.Items {
		addITCItem(w, l)
	}
	w.U8(1)
	return w.Packet()
}

// ITCEmptyList answers a list request with no items on the page.
func ITCEmptyList(req ITCListRequest) []byte {
	return ITCList(req, domain.ITCPage{})
}

// ITCStorage is the 拍賣倉庫 panel (0x55B9B3).
func ITCStorage(items []domain.ITCListing) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(ITCResultStorage)
	w.I32(int32(len(items)))
	for _, l := range items {
		addITCItem(w, l)
	}
	w.I32(0) // no "交易結束的道具共有%d個" notice
	w.U8(1)
	return w.Packet()
}

// ITCSelling is the 等待販售 panel (0x55B759).
func ITCSelling(items []domain.ITCListing) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(ITCResultSelling)
	w.I32(int32(len(items)))
	for _, l := range items {
		addITCItem(w, l)
	}
	return w.Packet()
}

// ITCResult is a result type with no body.
func ITCResult(kind byte) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(kind)
	return w.Packet()
}

// ITCResultCode is a result type followed by a failure code.
func ITCResultCode(kind, code byte) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(kind)
	w.U8(code)
	return w.Packet()
}

// ITCLimitFailure is a registration refused for a count limit.
func ITCLimitFailure(limit int) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(ITCResultRegisterFail)
	w.U8(ITCCodeLimit)
	w.U16(uint16(limit))
	return w.Packet()
}

// ITCTaken tells the client which inventory tab and slot received an item.
func ITCTaken(invType, slot int) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(ITCResultTaken)
	w.I32(int32(invType))
	w.I32(int32(slot))
	return w.Packet()
}

// ITCFailure shows the client's message for code and releases the request
// gate. Code 0 is the client's generic failure text.
func ITCFailure(code byte) []byte {
	return ITCResultCode(itcResultFailure, code)
}

// ITCSearchList answers a search (0x55B684): a list page without the sort
// bytes; it releases the request gate.
func ITCSearchList(req ITCListRequest, page domain.ITCPage) []byte {
	w := NewWriter(SendITCNormalItemResult)
	w.U8(ITCResultSearch)
	w.I32(int32(page.Total))
	w.I32(int32(len(page.Items)))
	w.I32(req.Tab)
	w.I32(req.Type)
	w.I32(req.Page)
	for _, l := range page.Items {
		addITCItem(w, l)
	}
	return w.Packet()
}

// ITCSearchRequest is action 5.
type ITCSearchRequest struct {
	Tab, Type  int32
	ByItemName bool
	Keyword    string
}

func ReadITCSearch(r *Reader) (ITCSearchRequest, error) {
	var v ITCSearchRequest
	v.Tab = r.I32()
	v.Type = r.I32()
	r.I32()
	v.ByItemName = r.I32() != 0
	v.Keyword = r.String()
	if err := r.Err(); err != nil {
		return v, err
	}
	if r.Remaining() != 0 || len(v.Keyword) > 64 {
		return v, ErrMalformed
	}
	return v, nil
}

// ITCWantedRequest is action 3 (購買登記).
type ITCWantedRequest struct {
	TemplateID int
	Price      int64
	Quantity   int
	Comment    string
}

func ReadITCWanted(r *Reader) (ITCWantedRequest, error) {
	var v ITCWantedRequest
	v.TemplateID = int(r.I32())
	v.Price = int64(r.I32())
	v.Quantity = int(r.I32())
	r.U8()
	r.U8()
	v.Comment = r.String()
	if err := r.Err(); err != nil {
		return v, err
	}
	if r.Remaining() != 0 || v.TemplateID <= 0 || len(v.Comment) > 64 {
		return v, ErrMalformed
	}
	return v, nil
}

// ITCFillRequest is action 2: selling an inventory item to a buy request.
// The item blob between the slot and the request id is not read.
type ITCFillRequest struct {
	InvType   int
	Slot      int
	ListingID int64
}

func ReadITCFill(r *Reader) (ITCFillRequest, error) {
	var v ITCFillRequest
	n := r.Remaining()
	if n < 1+4+1+4+4 || n > 4096 {
		return v, ErrMalformed
	}
	body := r.Bytes(n)
	if err := r.Err(); err != nil {
		return v, err
	}
	v.InvType = int(body[0])
	v.Slot = int(int32(binary.LittleEndian.Uint32(body[1:5])))
	v.ListingID = int64(int32(binary.LittleEndian.Uint32(body[n-4:])))
	if v.InvType < 1 || v.InvType > 4 || v.Slot < 1 || v.ListingID <= 0 {
		return v, ErrMalformed
	}
	return v, nil
}
