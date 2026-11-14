package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCashLockerFull = errors.New("cash locker is full")
	// Gift/purchase refusals join ErrForbidden so generic callers keep working
	// while the cash shop can still choose the exact native notice.
	ErrCashSameAccount = errors.New("cash gift recipient shares the sender account")
	ErrCashGender      = errors.New("cash item gender does not match")
)

type ServiceLease struct {
	Service, InstanceID string
	Epoch               int64
	ExpiresAt           time.Time
}
type CashSession struct {
	AccountName   string
	Session       CharacterSession
	Lease         ServiceLease
	NativeHandle  int64
	ReturnChannel int
	// ITC is set when the character came through the 拍賣 button: the cash
	// shop process serves the item trading center instead of the shop.
	ITC bool
}
type CashTransfer struct {
	ID                 string
	NativeHandle       int64
	WorldID, ChannelID int
	ExpiresAt          time.Time
}
type CashProduct struct {
	SN, TemplateID, Quantity, Gender, PeriodDays int
	Price                                        int64
	OnSale                                       bool
	LimitMode                                    int // WZ Limit: 2=first account purchase, 3=one purchase per SN.
}
type CashWallet struct{ Credit, Points, Prepaid int64 }
type CashItem struct {
	ID, AccountID, CharacterID              int64
	SN, TemplateID, Quantity, InvType, Slot int
	ExpiresAt                               int64
	State                                   string
	GiftFrom                                string
	Equipment                               *Equipment
}
type CashPurchaseRequest struct {
	Session      CashSession
	OperationID  string
	SN, Currency int
	Option       uint8
}
type CashPurchaseResult struct {
	Item     CashItem
	Wallet   CashWallet
	Replayed bool
}
type CashMoveRequest struct {
	Session     CashSession
	OperationID string
	CashID      int64
	ToLocker    bool
	InvType     int
	TargetSlot  int
}
type CashMoveResult struct {
	Item      CashItem
	Character Character
	Replayed  bool
}

// ITCTransferRepository sends a character from a channel to the item trading
// center, which the cash shop process serves.
type ITCTransferRepository interface {
	BeginITCTransfer(context.Context, CharacterSession, int64, string) (CashTransfer, error)
}

type CashRepository interface {
	AcquireCashLease(context.Context, string) (ServiceLease, error)
	RenewCashLease(context.Context, ServiceLease) (ServiceLease, error)
	ReleaseCashLease(context.Context, ServiceLease) error
	BeginCashTransfer(context.Context, CharacterSession, int64, string) (CashTransfer, error)
	ConsumeCashTransfer(context.Context, int64, string, ServiceLease) (CashSession, error)
	BeginCashReturn(context.Context, CashSession, string) (CashTransfer, error)
	ConsumeCashReturn(context.Context, int64, int, int, string, WorldLease) (CharacterSession, bool, error)
	ReleaseCashCharacter(context.Context, CashSession) error
	ReadCashWallet(context.Context, CashSession) (CashWallet, error)
	ReadCashLocker(context.Context, CashSession) ([]CashItem, error)
	PurchaseCash(context.Context, CashPurchaseRequest) (CashPurchaseResult, error)
	MoveCashItem(context.Context, CashMoveRequest) (CashMoveResult, error)
}

// CashLockerExpiryRepository retires expired locker records and returns them
// once, so the cash shop can show the native expiry notice exactly once.
type CashLockerExpiryRepository interface {
	ExpireCashLocker(context.Context, CashSession) ([]CashItem, error)
}

type CashExpiryRepository interface {
	ExpireCashInventory(context.Context, CharacterSession) (InventoryResult, error)
}
