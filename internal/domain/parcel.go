package domain

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Parcels are the 宅配 (Duey, NPC 9010009) service: one character sends
// another mesos and at most one item, which wait in the receiver's parcel box
// until the receiver takes or deletes them. The rules follow the v148 client
// (CParcelDlg, packet handler 0x658F2A): the box lists at most ten parcels, a
// parcel shows its days left out of thirty, a normal send costs the trade fee
// on the mesos plus 5,000 and a quick send (快速配送) the fee alone plus one
// 快遞使用券. A normal parcel arrives ParcelDelay after it is sent (the
// client's text says twelve hours; the server setting decides, default one
// hour); a quick one arrives at once. The thirty days count from arrival.
const (
	ParcelCapacity   = 10
	ParcelBaseFee    = 5000
	ParcelLifetime   = 30 * 24 * time.Hour
	ParcelQuickItem  = 5330000
	ParcelMaxMesos   = 2147483647
	ParcelMaxMessage = 199 // Big5 bytes; the parcel record keeps 200 with its terminator
)

// DefaultParcelDelay is the parcel_delay_minutes default.
const DefaultParcelDelay = time.Hour

var parcelDelay atomic.Int64

func init() { parcelDelay.Store(int64(DefaultParcelDelay)) }

// ParcelDelay is how long a normal parcel takes to arrive.
func ParcelDelay() time.Duration { return time.Duration(parcelDelay.Load()) }

// SetParcelDelay changes it for parcels sent from now on.
func SetParcelDelay(d time.Duration) {
	if d < 0 {
		d = 0
	}
	parcelDelay.Store(int64(d))
}

var (
	// ErrParcelInTransit: the parcel has not arrived yet.
	ErrParcelInTransit = errors.New("parcel still in transit")
	// ErrParcelRecipient: no character of that name in this world.
	ErrParcelRecipient = errors.New("parcel recipient not found")
	// ErrParcelSameAccount: the recipient shares the sender's account.
	ErrParcelSameAccount = errors.New("parcel recipient shares the sender account")
	// ErrParcelBoxFull: the recipient already holds ParcelCapacity parcels.
	ErrParcelBoxFull = errors.New("parcel box full")
	// ErrParcelOnlyOne: a one-of-a-kind item the other side already has.
	ErrParcelOnlyOne = errors.New("parcel one-of-a-kind item already held")
	// ErrParcelNoRoom: the receiver's inventory cannot hold the item.
	ErrParcelNoRoom = errors.New("parcel item does not fit")
)

type Parcel struct {
	ID         int64          `json:"id"`
	SenderID   int64          `json:"sender_id"`
	SenderName string         `json:"sender_name"`
	ReceiverID int64          `json:"receiver_id"`
	Mesos      int64          `json:"mesos"`
	Item       *InventoryItem `json:"item,omitempty"`
	Equipment  *Equipment     `json:"equipment,omitempty"`
	Quick      bool           `json:"quick"`
	Message    string         `json:"message"`
	SentAt     time.Time      `json:"sent_at"`
	ArrivesAt  time.Time      `json:"arrives_at"`
	ExpiresAt  time.Time      `json:"expires_at"`
}

// ParcelBox is what the parcel window opens with: the parcels waiting, and
// the ones that expired since the last look (already gone, listed once).
type ParcelBox struct {
	Parcels []Parcel `json:"parcels"`
	Expired []Parcel `json:"expired"`
}

type ParcelSend struct {
	Session     CharacterSession
	OperationID string
	// InvType 0 sends mesos only.
	InvType   int
	Slot      int
	Quantity  int
	Mesos     int64
	Recipient string
	Quick     bool
	Message   string
	// CouponSlot is the cash inventory slot of the 快遞使用券 a quick send uses.
	CouponSlot int
}

type ParcelSendResult struct {
	Inventory InventoryResult `json:"inventory"`
	Parcel    Parcel          `json:"parcel"`
	Cost      int64           `json:"cost"`
}

type ParcelTake struct {
	Session     CharacterSession
	OperationID string
	ParcelID    int64
	// Delete throws the parcel away instead of taking its contents.
	Delete bool
}

type ParcelTakeResult struct {
	Inventory InventoryResult `json:"inventory"`
	Parcel    Parcel          `json:"parcel"`
}

type ParcelRepository interface {
	OpenParcels(context.Context, CharacterSession) (ParcelBox, error)
	SendParcel(context.Context, ParcelSend) (ParcelSendResult, error)
	TakeParcel(context.Context, ParcelTake) (ParcelTakeResult, error)
	// HasQuickParcel reports a quick parcel waiting for the character, which
	// lets it open the box from the arrival notice anywhere.
	HasQuickParcel(context.Context, CharacterSession) (bool, error)
}

// ParcelFee is what sending mesos by parcel costs on top of the mesos.
func ParcelFee(mesos int64, quick bool) int64 {
	fee := TradeFee(mesos)
	if !quick {
		fee += ParcelBaseFee
	}
	return fee
}
