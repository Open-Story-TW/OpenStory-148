package cashshop

import (
	"errors"

	"maple-go-server/internal/domain"
)

// Native action104 failure reasons, read from the v145 common handler 0x47BF4C
// and its decrypted StringPool templates (see docs/baseline/native-cash-failures.json).
// Legacy Java numbers (0xA2, 0xA3, 0xB1...) belong to an older client and do not apply.
const (
	reasonInsufficient     byte = 3  // 632 樂豆點餘額不足
	reasonGiftSameAccount  byte = 6  // 4911 無法送禮到相同的帳號
	reasonGiftNoRecipient  byte = 7  // 4912 錯誤的角色名稱
	reasonGiftGender       byte = 8  // 4913 請確認收禮人的性別
	reasonLockerFull       byte = 10 // 636 超過可以保有的加值道具數量
	reasonGiftCheck        byte = 11 // 637 recipient server/name/gender/locker check
	reasonUnknown          byte = 12 // no case: default 659 發生不明錯誤
	reasonOwnGender        byte = 21 // 640 你的性別無法使用這項道具
	reasonInventoryFull    byte = 24 // 998 道具欄的空間不夠
	reasonSecondPassword   byte = 32 // 620 請確認第二組密碼
	reasonCouponInvalid    byte = 14 // 646 此序號發生異常
	reasonCouponUsed       byte = 16 // 648 此序號已被使用過
	reasonGiftInvalidInput      = reasonGiftCheck
)

func cashPurchaseFailure(err error) byte {
	switch {
	case errors.Is(err, domain.ErrCashLockerFull):
		return reasonLockerFull
	case errors.Is(err, domain.ErrCashGender):
		return reasonOwnGender
	case errors.Is(err, domain.ErrInsufficient):
		return reasonInsufficient
	default:
		return reasonUnknown
	}
}

// cashFeatureFailure maps gift, gift-package and package refusals.
func cashFeatureFailure(kind string, err error) byte {
	gift := kind == "gift" || kind == "gift_package"
	switch {
	case kind == "coupon":
		return cashCouponFailure(err)
	case errors.Is(err, domain.ErrCashLockerFull):
		return reasonLockerFull
	case errors.Is(err, domain.ErrCashSameAccount):
		return reasonGiftSameAccount
	case errors.Is(err, domain.ErrCashGender) && gift:
		return reasonGiftGender
	case errors.Is(err, domain.ErrCashGender):
		return reasonOwnGender
	case errors.Is(err, domain.ErrBadCredentials):
		return reasonSecondPassword
	case errors.Is(err, domain.ErrNotFound) && gift:
		return reasonGiftNoRecipient
	case errors.Is(err, domain.ErrInsufficient):
		return reasonInsufficient
	case errors.Is(err, domain.ErrInvalidInput) && gift:
		return reasonGiftInvalidInput
	default:
		return reasonUnknown
	}
}

// cashMoveFailure: locker->inventory lacks a slot, inventory->locker is full.
func cashMoveFailure(toLocker bool, err error) byte {
	switch {
	case errors.Is(err, domain.ErrCashLockerFull):
		return reasonLockerFull
	case !toLocker && errors.Is(err, domain.ErrInsufficient):
		return reasonInventoryFull
	default:
		return reasonUnknown
	}
}

// Native action104 reason16 is StringPool648 (serial already used), while
// reason14 is StringPool646 (invalid serial/contact support). Reason9 is a
// recipient's full gift locker and must never label a coupon replay.
func cashCouponFailure(err error) byte {
	if errors.Is(err, domain.ErrAlreadyExists) {
		return reasonCouponUsed
	}
	return reasonCouponInvalid
}
