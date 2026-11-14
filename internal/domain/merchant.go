package domain

import (
	"context"
	"math"
	"strconv"
	"sync/atomic"
	"time"
)

// Hired merchant states. A shop that is not MerchantClosed is on a map (or
// being set up there); a closed shop is the owner's package at Fredrick.
const (
	MerchantSetup       = "setup"
	MerchantOpen        = "open"
	MerchantMaintenance = "maintenance"
	MerchantClosed      = "closed"
)

// MerchantItemLimit is the native entrusted shop item list size.
const MerchantItemLimit = 16

// MerchantDuration is how long one permit keeps a shop on the map (Legacy).
const MerchantDuration = 24 * time.Hour

// MerchantItem is one sale row: Bundles lots of PerBundle items at Price per
// lot. Item.Quantity mirrors PerBundle. A sold-out row keeps Bundles 0 until
// the owner tidies the shop, as the native list shows it.
type MerchantItem struct {
	ID        int64         `json:"id"`
	Position  int           `json:"position"`
	Bundles   int           `json:"bundles"`
	PerBundle int           `json:"per_bundle"`
	Price     int64         `json:"price"`
	Item      InventoryItem `json:"item"`
	Equipment *Equipment    `json:"equipment,omitempty"`
}

// MerchantSale is one line of the owner's sold-items list.
type MerchantSale struct {
	TemplateID int    `json:"template_id"`
	Bundles    int    `json:"bundles"`
	PerBundle  int    `json:"per_bundle"`
	Total      int64  `json:"total"`
	BuyerID    int64  `json:"buyer_id"`
	Buyer      string `json:"buyer"`
}

type MerchantShop struct {
	ID           int64          `json:"id"`
	CharacterID  int64          `json:"character_id"`
	AccountID    int64          `json:"account_id"`
	WorldID      int            `json:"world_id"`
	ChannelID    int            `json:"channel_id"`
	MapID        int            `json:"map_id"`
	X            int            `json:"x"`
	Y            int            `json:"y"`
	Foothold     int            `json:"foothold"`
	PermitItemID int            `json:"permit_item_id"`
	OwnerName    string         `json:"owner_name"`
	Title        string         `json:"title"`
	State        string         `json:"state"`
	Mesos        int64          `json:"mesos"` // income not yet collected; 64-bit
	Revision     int64          `json:"revision"`
	OpenedAt     time.Time      `json:"opened_at"`
	ExpiresAt    time.Time      `json:"expires_at"`
	Items        []MerchantItem `json:"items"`
	Sales        []MerchantSale `json:"sales"`
}

// Stock reports whether anything is still for sale.
func (m MerchantShop) Stock() bool {
	for _, item := range m.Items {
		if item.Bundles > 0 {
			return true
		}
	}
	return false
}

type MerchantCreate struct {
	Session      CharacterSession
	PermitSlot   int
	PermitItemID int
	Title        string
	Foothold     int
}

// MerchantRequest is one owner or buyer command on a shop. Kind:
//
//	put      owner moves Bundles*PerBundle of an inventory slot into the shop
//	take     owner takes row ItemID back
//	buy      visitor buys Bundles lots of row ItemID at the Price it was shown
//	collect  owner takes the income and drops sold-out rows
//	reclaim  owner takes every item and the income; the shop row is deleted
type MerchantRequest struct {
	Session          CharacterSession
	ShopID           int64
	ExpectedRevision int64
	Kind             string
	InvType          int
	Slot             int
	Bundles          int
	PerBundle        int
	Price            int64
	ItemID           int64
}

type MerchantResult struct {
	Inventory InventoryResult `json:"inventory"`
	Shop      MerchantShop    `json:"shop"`
	Sale      *MerchantSale   `json:"sale,omitempty"`
	Deleted   bool            `json:"deleted"` // reclaim emptied and removed the shop
}

// MerchantRepository keeps hired merchants. Owner and buyer commands are
// fenced by the caller's live session; state changes are not, because a shop
// outlives its owner's session (expiry, restart recovery).
type MerchantRepository interface {
	// MerchantFor returns the account's shop or Fredrick package in a world.
	MerchantFor(ctx context.Context, accountID int64, worldID int) (MerchantShop, bool, error)
	CreateMerchant(context.Context, MerchantCreate) (MerchantShop, error)
	ChangeMerchant(context.Context, MerchantRequest) (MerchantResult, error)
	// SetMerchantState moves a shop between states; from lists the states it
	// may currently be in. An empty closed shop is deleted (found=false).
	SetMerchantState(ctx context.Context, shopID int64, from []string, to string) (MerchantShop, bool, error)
	// ActiveMerchants lists every shop of a world that is not closed.
	ActiveMerchants(ctx context.Context, worldID int) ([]MerchantShop, error)
}

var merchantCashItems atomic.Bool

// MerchantCashItems reports whether cash items may be put on sale.
func MerchantCashItems() bool { return merchantCashItems.Load() }

// SetMerchantCashItems switches cash items in hired merchants.
func SetMerchantCashItems(enabled bool) { merchantCashItems.Store(enabled) }

// SetMerchantCashItemsForTest switches the setting and returns a restore function.
func SetMerchantCashItemsForTest(enabled bool) (restore func()) {
	previous := merchantCashItems.Swap(enabled)
	return func() { merchantCashItems.Store(previous) }
}

// MerchantMaxPrice is the highest price of one lot. Under the long money
// contract the merchant price fields are 64-bit on the wire; otherwise the
// native 32-bit fields bound it.
func MerchantMaxPrice() int64 {
	if LongMoneyEnabled() {
		return math.MaxInt64
	}
	return math.MaxInt32
}

// MerchantTotal is price x lots, or false when it does not fit in 64 bits.
func MerchantTotal(price int64, lots int) (int64, bool) {
	if price < 1 || lots < 1 || price > math.MaxInt64/int64(lots) {
		return 0, false
	}
	return price * int64(lots), true
}

// MoneyUnits writes an amount with the units players read prices in, so a
// long number is not misread: 5130042002123 is "5兆1300億4200萬2123".
func MoneyUnits(amount int64) string {
	if amount < 0 {
		if amount == math.MinInt64 {
			return strconv.FormatInt(amount, 10)
		}
		return "-" + MoneyUnits(-amount)
	}
	if amount < 10000 {
		return strconv.FormatInt(amount, 10)
	}
	units := []struct {
		size int64
		name string
	}{{1_0000_0000_0000_0000, "京"}, {1_0000_0000_0000, "兆"}, {1_0000_0000, "億"}, {1_0000, "萬"}}
	out := ""
	for _, unit := range units {
		if part := amount / unit.size; part > 0 {
			out += strconv.FormatInt(part, 10) + unit.name
			amount %= unit.size
		}
	}
	if amount > 0 {
		out += strconv.FormatInt(amount, 10)
	}
	return out
}
