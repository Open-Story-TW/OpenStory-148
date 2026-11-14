package domain

import (
	"context"
	"errors"
	"time"
)

// The item trading center (拍賣). A seller lists an item for 楓葉點數 as a
// direct sale or as an auction. A buyer pays the price plus the commission;
// a bidder pays the bid plus the commission in advance and gets it back when
// outbid. The item waits in the buyer's or winner's 拍賣倉庫 until it is
// taken into the inventory; cancelled and unsold listings go back to the
// seller's 拍賣倉庫.

// ITC listing states.
const (
	ITCSelling = "selling" // on the 販售 list, owned by the seller
	ITCStorage = "storage" // in the owner's 拍賣倉庫
)

// ITC numbers the client shows and checks (CITC header, 0x55A0C7).
const (
	ITCRegistrationFee = 5000               // mesos, kept even when the sale is cancelled
	ITCCommissionRate  = 5                  // percent the buyer pays on top of the price
	ITCCommissionBase  = 0                  // flat amount the buyer pays on top
	ITCMinPrice        = 20                 // 請輸入20楓點以上的價格
	ITCMaxPrice        = 10_000_000 - 1     // the client's own upper bound (0x558FF8)
	ITCSaleDuration    = 7 * 24 * time.Hour // 販售的登錄刊登時間為七日
	ITCSellingLimit    = 10                 // listings one character may have at once
	ITCPageSize        = 16                 // rows per list page (0x565312)
	ITCCartLimit       = 50
)

// ITCCommission is what the buyer pays on top of the price.
func ITCCommission(price int64) int64 {
	return price*ITCCommissionRate/100 + ITCCommissionBase
}

type ITCListing struct {
	ID         int64
	WorldID    int
	SellerID   int64
	SellerName string
	OwnerID    int64 // who may take it from 拍賣倉庫 (state storage)
	State      string
	Item       InventoryItem
	Equipment  *Equipment
	Price      int64 // sale price, or an auction's starting price
	ListedAt   time.Time
	EndsAt     time.Time
	// Auctions only.
	Kind         string // ITCSale or ITCAuction
	BuyNowPrice  int64
	BidStep      int
	BidCount     int
	CurrentPrice int64 // highest bid; the starting price before any bid
	HighBidderID int64
	Comment      string // buy requests
	RequestID    int64  // offers: the buy request
	// Status is the 交易狀態 the 我的拍賣 rows show (ITCITEM +28): on 交易紀錄
	// 0 販售, 1 購買, 2 未得標, 3 流標; on 交易進行 1 競標中, 2 下標.
	Status int
}

// ITC listing kinds.
const (
	ITCSale    = "sale"
	ITCAuction = "auction"
	ITCWanted  = "wanted"  // a buy request (購買登記): SellerID is the buyer
	ITCOffer   = "offer"   // an item sold to a request, waiting for the buyer
	ITCHistory = "history" // a 交易紀錄 row (never stored as a listing)
)

// Commission is the buyer's surcharge on this listing.
func (l ITCListing) Commission() int64 { return ITCCommission(l.Price) }

// MinimumBid is the lowest bid the auction accepts now: the starting price
// for the first bid, then the current bid plus the step.
func (l ITCListing) MinimumBid() int64 {
	if l.BidCount == 0 {
		return l.Price
	}
	return l.CurrentPrice + int64(max(l.BidStep, 1))
}

// ITCView names a list the client asks for (action 4: tab, type, page).
type ITCView struct {
	Tab, Type, Page int
	// Search (action 5) narrows a tab by the lister's name or by items.
	Search    bool
	Name      string
	Templates []int
}

// ITCWantedShare is the percent of a buy request's price the seller gets
// (6967: 將扣除手續費10%的金額，告知賣方).
const ITCWantedShare = 90

// ITCWantedPayout is what a seller filling a buy request receives.
func ITCWantedPayout(price int64) int64 { return price * ITCWantedShare / 100 }

type ITCPage struct {
	Total int
	Items []ITCListing
}

// ITCRequest is one trading center command from the cash shop process.
type ITCRequest struct {
	Session     CashSession
	OperationID string
	Kind        string // register, auction, bid, buy_now, buy, cancel, take, cart_add, cart_remove, wanted, fill, withdraw
	ListingID   int64
	// register and auction
	InvType, Slot, TemplateID, Quantity int
	Price                               int64 // sale price, starting price, or a bid
	BuyNowPrice                         int64
	BidStep                             int
	Hours                               int
	Comment                             string // wanted
}

// ITC auction bounds (6986: 24~168 hours, set by the hour).
const (
	ITCMinHours = 24
	ITCMaxHours = 168
)

type ITCResult struct {
	Inventory InventoryResult
	Wallet    CashWallet
	Listing   ITCListing
}

type ITCRepository interface {
	// ITCList reads one page of a tab for the session's character.
	ITCList(context.Context, CashSession, ITCView) (ITCPage, error)
	// ITCSelling is the character's own listings still on sale (等待販售).
	ITCSelling(context.Context, CashSession) ([]ITCListing, error)
	// ITCStorage is what waits in the character's 拍賣倉庫.
	ITCStorage(context.Context, CashSession) ([]ITCListing, error)
	ChangeITC(context.Context, ITCRequest) (ITCResult, error)
}

// ErrITCHasBids refuses to cancel an auction somebody has bid on.
var ErrITCHasBids = errors.New("auction has bids")

// ErrITCMesos is a registration fee the character cannot pay in mesos
// (楓幣不足), told apart from missing 楓葉點數 on a buy request.
var ErrITCMesos = errors.New("mesos below the registration fee")
