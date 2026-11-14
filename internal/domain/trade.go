package domain

import (
	"context"
	"sync/atomic"
)

var tradeCashItems atomic.Bool

// TradeCashItems reports whether cash items may go into a player trade.
func TradeCashItems() bool { return tradeCashItems.Load() }

// SetTradeCashItems switches cash items in player trades.
func SetTradeCashItems(enabled bool) { tradeCashItems.Store(enabled) }

// SetTradeCashItemsForTest switches the setting and returns a restore function.
func SetTradeCashItemsForTest(enabled bool) (restore func()) {
	previous := tradeCashItems.Swap(enabled)
	return func() { tradeCashItems.Store(previous) }
}

// TradeCashAllowed reports whether one cash item may be traded: only while
// the operator allows it, only with its serial, and never a pet (500xxxx) or
// a shop permit (503xxxx), the same families a hired merchant refuses.
func TradeCashAllowed(template int, cashID int64) bool {
	return TradeCashItems() && cashID > 0 && template/10000 != 500 && template/10000 != 503
}

type TradeItem struct {
	InvType    int            `json:"inv_type"`
	Slot       int            `json:"slot"`
	Quantity   int            `json:"quantity"`
	InstanceID int64          `json:"instance_id"`
	TradeSlot  int            `json:"trade_slot"`
	Equipment  *Equipment     `json:"equipment,omitempty"`
	Item       *InventoryItem `json:"item,omitempty"`
}

type TradeOffer struct {
	Mesos int64       `json:"mesos"`
	Items []TradeItem `json:"items"`
}
type TradeSettlement struct {
	ID                      string
	First, Second           CharacterSession
	FirstOffer, SecondOffer TradeOffer
}
type TradeResult struct {
	First     InventoryResult `json:"first"`
	Second    InventoryResult `json:"second"`
	FirstTax  int64           `json:"first_tax"`
	SecondTax int64           `json:"second_tax"`
	Replayed  bool            `json:"replayed"`
}
type TradeRepository interface {
	SettleTrade(context.Context, TradeSettlement) (TradeResult, error)
}

// TradeReceiptRepository resolves a lost commit response for the exact original
// operation, without attempting another debit/grant. committed=false is only
// authoritative after waiting for both ordered character locks held by the
// original transaction. An unavailable/stale result remains unknown.
type TradeReceiptRepository interface {
	ResolveTradeReceipt(context.Context, TradeSettlement) (TradeResult, bool, error)
}
