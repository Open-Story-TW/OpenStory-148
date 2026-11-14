package protocol

import (
	"maple-go-server/internal/domain"
	"strings"
)

const RecvCashCoupon uint16 = 0x1b4
const RecvCashGift uint16 = 0x1b5

// Native 469441 sends action46 followed by one SN; it is a catalogue query,
// not a purchase. 47A6E4 consumes the matching CashPurchaseRecord response.
func ReadNativeCashPurchaseRecord(r *Reader) (int, error) {
	sn := int(r.I32())
	if r.Err() != nil || r.Remaining() != 0 || sn < 0 {
		return 0, ErrMalformed
	}
	return sn, nil
}

// Native 47A97B walks the received-gift list and, per gift, sends action60
// (469B00, dismissed) or action61 (469B67, after a 0xD2 thank-you note) with
// the gift's 8-byte cash serial. Both set the pending-request flag, so the
// caller must answer with the normal locker refresh, never a failure.
func ReadNativeCashGiftAck(r *Reader) (int64, error) {
	serial := r.I64()
	if r.Err() != nil || r.Remaining() != 0 {
		return 0, ErrMalformed
	}
	return serial, nil
}

// GiftNote is the native 47AAAF thank-you memo: type 0, sender name, memo,
// fame flag, gift list index, then the gift's cash serial.
type GiftNote struct {
	To      string
	Message string
	Fame    bool
	Serial  int64
}

func ReadNativeGiftNote(r *Reader) (GiftNote, error) {
	if r.U8() != 0 {
		return GiftNote{}, ErrMalformed
	}
	v := GiftNote{To: r.String(), Message: r.String(), Fame: r.U8() != 0}
	r.I32()
	v.Serial = r.I64()
	if r.Err() != nil || r.Remaining() != 0 || v.To == "" {
		return GiftNote{}, ErrMalformed
	}
	return v, nil
}

// Native standard purchase at 0x46F47C: action3 is consumed by the caller.
// The option is retained for explicit policy validation, never silently lost.
func ReadNativeCashPurchase(r *Reader) (domain.CashPurchaseRequest, error) {
	currency := r.U8()
	v := domain.CashPurchaseRequest{Currency: int(currency) + 1, SN: int(r.I32()), Option: r.U8()}
	if r.Err() != nil || r.Remaining() != 0 || currency > 1 || v.SN < 1 {
		return v, ErrMalformed
	}
	return v, nil
}

// Native 0x46A83C and 0x46AA1D include the inventory type; withdrawal also
// includes the exact positive destination the native client found empty.
func ReadNativeCashMove(action byte, r *Reader) (domain.CashMoveRequest, error) {
	v := domain.CashMoveRequest{CashID: r.I64(), InvType: int(r.U8()), ToLocker: action == 15}
	if action == 14 {
		v.TargetSlot = int(r.I16())
		if v.TargetSlot < 1 || v.TargetSlot > domain.MaxInventorySlots {
			return v, ErrMalformed
		}
	} else if action != 15 {
		return v, ErrMalformed
	}
	if r.Err() != nil || r.Remaining() != 0 || v.CashID < 1 || v.InvType < 1 || v.InvType > 5 {
		return v, ErrMalformed
	}
	return v, nil
}

// ReadNativeCashGift follows the raw v145 encoders at 0x4736DD..0x47399D
// and 0x4784B0..0x4785BE. Ordinary gifts have a flag before the PIC;
// package gifts omit that first flag. Both end with a gift option byte.
func ReadNativeCashGift(action byte, r *Reader) (domain.CashFeatureRequest, error) {
	v := domain.CashFeatureRequest{Kind: "gift"}
	if action == 4 {
		if r.U8() > 1 {
			return v, ErrMalformed
		}
	} else if action == 34 {
		v.Kind = "gift_package"
	} else {
		return v, ErrMalformed
	}
	v.PIN = r.String()
	v.SN = int(r.I32())
	v.Recipient = r.String()
	v.Message = r.String()
	if r.U8() > 1 || r.Err() != nil || r.Remaining() != 0 {
		return v, ErrMalformed
	}
	return v, nil
}

// Native 0x195 builder at 0x471F71..0x4720B0 uses PIC, SN, recipient,
// message. It is distinct from the 0x192 action prefix.
func ReadNativeDirectGift(r *Reader) (domain.CashFeatureRequest, error) {
	v := domain.CashFeatureRequest{Kind: "gift", PIN: r.String(), SN: int(r.I32()), Recipient: r.String(), Message: r.String()}
	if r.Err() != nil || r.Remaining() != 0 {
		return v, ErrMalformed
	}
	return v, nil
}

// Native coupon dialog handler0x4746E2 emits 0x194, mode0, two credential
// strings, and an option byte. Server-issued two-part credentials use a colon;
// neither field is treated as authority to mint currency.
func ReadNativeCashCoupon(r *Reader) (domain.CashFeatureRequest, error) {
	v := domain.CashFeatureRequest{Kind: "coupon"}
	if r.U8() != 0 {
		return v, ErrMalformed
	}
	first, second := r.String(), r.String()
	_ = r.U8()
	v.Coupon = strings.TrimSpace(first)
	if second != "" {
		v.Coupon += ":" + second
	}
	if r.Err() != nil || r.Remaining() != 0 || len(v.Coupon) < 1 || len(v.Coupon) > 64 {
		return v, ErrMalformed
	}
	return v, nil
}

func CashWishlistItems(items [10]int, update bool) []byte {
	w := NewWriter(SendCashOperation)
	if update {
		w.U8(101)
	} else {
		w.U8(99)
	}
	for _, sn := range items {
		w.I32(int32(sn))
	}
	return w.Packet()
}

func CashGiftItems(gifts []domain.CashGift) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(97)
	w.U16(uint16(len(gifts)))
	for _, gift := range gifts {
		w.I64(gift.Item.ID)
		w.I32(int32(gift.Item.TemplateID))
		w.FixedString(gift.Sender, 15)
		w.FixedString(gift.Message, 74)
	}
	return w.Packet()
}

func CashGiftSent(result domain.CashFeatureResult) []byte {
	w := NewWriter(SendCashOperation)
	if result.Package {
		w.U8(159) // native 0x47AD9A: string, int32, uint16, uint16.
	} else {
		w.U8(110) // native 0x47AEFA: string, int32, uint16.
	}
	w.String(result.Recipient)
	w.I32(int32(result.Product.TemplateID))
	w.U16(uint16(result.Product.Quantity))
	if result.Package {
		w.U16(0)
	}
	return w.Packet()
}

func CashPackageBought(items []domain.CashItem) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(157) // native 0x47ABB8; Java's 171 has no native dispatcher case.
	w.U8(byte(len(items)))
	for _, item := range items {
		cashItemInfo(w, item)
	}
	w.U16(0)
	w.I32(0)
	return w.Packet()
}

func CashCouponRedeemed(result domain.CashFeatureResult) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(105) // native 0x47D8CB; 119 is a failure parser in this client.
	w.U8(byte(len(result.Items)))
	for _, item := range result.Items {
		cashItemInfo(w, item)
	}
	w.I32(int32(result.CreditAward))
	w.I32(0)
	w.I32(int32(result.MesosAward))
	return w.Packet()
}

func CashPurchaseRecord(sn int, purchased bool) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(178) // native 0x47A6E4: one product ownership flag, not history entries.
	w.I32(int32(sn))
	w.Bool(purchased)
	return w.Packet()
}

// CashItemExpired is native 0x47B1D5 (Java's obsolete operation 142): an 8-byte
// cash serial removed from the locker list with StringPool 3284 naming the item.
// An unknown serial still opens a notice with an empty name, so only send it
// for records the preceding locker packet listed.
func CashItemExpired(cashID int64) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(128)
	w.I64(cashID)
	return w.Packet()
}

// Native request builders confirmed in the v145 IDB. Legacy action0 coupon
// input is not supported: the native dialog uses the separate 0x194 packet.
func CashActionWireReady(action byte) bool {
	switch action {
	case 3, 4, 5, 14, 15, 33, 34, 46:
		return true
	}
	return false
}

func CashLimitFailure(sn int) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(104)
	w.U8(29)
	w.I32(int32(sn))
	return w.Packet()
}
