package domain

import (
	"context"
	"errors"
)

var ErrCashLimit = errors.New("cash purchase limit reached")

type CashFeatureRequest struct {
	Session                         CashSession
	OperationID                     string
	Kind                            string
	SN                              int
	Wishlist                        [10]int
	Recipient, Message, PIN, Coupon string
}

type CashGift struct {
	Item            CashItem
	Sender, Message string
}

type CashFeatureResult struct {
	Wallet                  CashWallet
	Items                   []CashItem
	Wishlist                [10]int
	Recipient               string
	Product                 CashProduct
	Package                 bool
	Character               Character
	Changes                 []InventoryChange
	CreditAward, MesosAward int64
	Replayed                bool
}

type CashFeaturesRepository interface {
	ReadCashWishlist(context.Context, CashSession) ([10]int, error)
	ChangeCashFeature(context.Context, CashFeatureRequest) (CashFeatureResult, error)
	ClaimCashGifts(context.Context, CashSession) ([]CashGift, error)
}

// Account-scoped successful spending history, not current physical ownership.
// SN zero asks whether any purchase (including sent gifts/packages) committed.
type CashPurchaseHistoryRepository interface {
	ReadCashPurchaseRecord(context.Context, CashSession, int) (bool, error)
}
