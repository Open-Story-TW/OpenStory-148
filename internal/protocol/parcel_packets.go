package protocol

import (
	"time"

	"golang.org/x/text/encoding/traditionalchinese"

	"maple-go-server/internal/domain"
)

// 宅配 (Duey) on v148. The client's parcel window is CParcelDlg (constructor
// 0x656D79, background UI/UIWindow2.img/Delivery); its server packet handler
// is 0x658F2A (CField::OnPacket 0x51F0E7 case 0x25E; Open-Maple docs/server/v148/opcode-map.json maps Legacy DUEY to 0x260, which is wrong) and every request goes out as RecvParcel:
//
//	1  answer to the second password check (op 9), not used here
//	3  send: invType u8, slot u16, count u16, mesos i32, recipient str,
//	   quick u8, and for a quick send message str + coupon slot i32
//	   (0x65685D normal, 0x656274 quick)
//	5  take parcel i32 (0x655425)
//	6  delete parcel i32 (0x655563)
//	8  close (0x65596B)
//	0  open from a quick parcel's arrival notice: i32 -1, i32 2 (0x4FD6A0)
const (
	RecvParcel uint16 = 0x70
	SendParcel uint16 = 0x25E
)

// Parcel window result codes (default branch of 0x658F2A -> 0x6559BB). The
// text is the client's StringPool entry for each.
const (
	ParcelNoMesos      = 12 // 9439 楓幣不足！
	ParcelBadRequest   = 13 // 4405 此為不正確的要求！
	ParcelNoRecipient  = 14 // 4406 請重新確認收件人的名稱！
	ParcelSameAccount  = 15 // 4407 無法送件給同一帳號內的角色！
	ParcelBoxFull      = 16 // 4408 收件人的宅配保管箱已滿！
	ParcelCannotTake   = 17 // 4409 該角色無法收取宅配！
	ParcelOnlyInBox    = 18 // 4410 對方宅配保管箱內，有數量限制的道具！
	ParcelSent         = 19 // 4403 送出成功！
	ParcelUnknownError = 20 // 4411 發生了不明的錯誤！
	ParcelNoRoom       = 22 // 4412 請確認是否有空的欄位！
	ParcelOnlyHeld     = 23 // 4413 因其中有擁有數量限制道具，因此無法領取楓幣和道具！
)

const (
	parcelOpen    = 10
	parcelRemoved = 24
	parcelArrived = 26
	// parcelRecordSize is the fixed record 0x4CFFAA reads before the item.
	parcelRecordSize = 236
)

// OpenParcels opens the parcel window (op 10, 0x658F1F): fromNotice keeps it
// on the receive tab (mode 2), then the parcels waiting and the parcels that
// expired, each a 236-byte record and an optional GW_ItemSlotBase.
func OpenParcels(box domain.ParcelBox, fromNotice bool) []byte {
	w := NewWriter(SendParcel)
	w.U8(parcelOpen)
	w.Bool(fromNotice)
	parcels := box.Parcels
	if len(parcels) > domain.ParcelCapacity {
		parcels = parcels[:domain.ParcelCapacity]
	}
	w.U8(byte(len(parcels)))
	for _, p := range parcels {
		writeParcel(w, p)
	}
	expired := box.Expired
	if len(expired) > 255 {
		expired = expired[:255]
	}
	w.U8(byte(len(expired)))
	for _, p := range expired {
		writeParcel(w, p)
	}
	return w.Packet()
}

// ParcelResult is a one-byte result the window shows as a notice. A send's
// success (19) also clears the send tab.
func ParcelResult(code byte) []byte {
	w := NewWriter(SendParcel)
	w.U8(code)
	return w.Packet()
}

// ParcelRemoved drops a parcel from the open window: 刪除成功！ when deleted,
// 收件成功！ when taken.
func ParcelRemoved(id int64, deleted bool) []byte {
	w := NewWriter(SendParcel)
	w.U8(parcelRemoved)
	w.I32(int32(id))
	if deleted {
		w.U8(3)
	} else {
		w.U8(4)
	}
	return w.Packet()
}

// ParcelArrived is the arrival notice (op 26). A quick parcel's notice stays
// until clicked and opens the box from anywhere; a normal one fades.
func ParcelArrived(sender string, quick bool) []byte {
	w := NewWriter(SendParcel)
	w.U8(parcelArrived)
	w.String(sender)
	w.Bool(quick)
	return w.Packet()
}

// writeParcel is the record CParcelDlg keeps per parcel: id +0, sender +4
// (15 bytes), mesos +19, expiry FILETIME +23 (the list shows days left; at
// thirty or more it reads 運送中 and cannot be taken yet, which is how a
// parcel in transit shows: its expiry is thirty days after its arrival), quick flag +31,
// message +35 (200 bytes), then has-item and the item.
func writeParcel(w *Writer, p domain.Parcel) {
	start := len(w.data)
	w.I32(int32(p.ID))
	w.FixedString(clipBig5(p.SenderName, 15), 15)
	w.I32(int32(p.Mesos))
	expires := p.ExpiresAt
	if expires.IsZero() {
		expires = time.Now().Add(domain.ParcelLifetime)
	}
	// One second under the lifetime so a parcel sent this instant counts
	// twenty-nine days left rather than thirty (still in transit).
	w.I64(mapleTime(expires.Add(-time.Second)))
	if p.Quick {
		w.I32(1)
	} else {
		w.I32(0)
	}
	w.FixedString(clipBig5(p.Message, domain.ParcelMaxMessage), 200)
	w.Zero(parcelRecordSize - (len(w.data) - start))
	switch {
	case p.Equipment != nil:
		w.U8(1)
		addEquipment(w, *p.Equipment)
	case p.Item != nil:
		w.U8(1)
		addBundleItem(w, *p.Item)
	default:
		w.U8(0)
	}
}

// ParcelSendRequest is op 3.
type ParcelSendRequest struct {
	InvType    int
	Slot       int
	Quantity   int
	Mesos      int64
	Recipient  string
	Quick      bool
	Message    string
	CouponSlot int
}

func ReadParcelSend(r *Reader) (ParcelSendRequest, error) {
	q := ParcelSendRequest{InvType: int(r.U8()), Slot: int(r.I16()), Quantity: int(r.I16()), Mesos: int64(r.I32()), Recipient: r.String()}
	q.Quick = r.U8() != 0
	if q.Quick {
		q.Message = r.String()
		q.CouponSlot = int(r.I32())
	}
	if r.Err() != nil {
		return q, r.Err()
	}
	if r.Remaining() != 0 {
		return q, ErrMalformed
	}
	return q, nil
}

// clipBig5 cuts s to at most n Big5 bytes without splitting a character.
// A string Big5 cannot encode comes back empty.
func clipBig5(s string, n int) string {
	runes := []rune(s)
	for len(runes) > 0 {
		p, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(string(runes)))
		if err != nil {
			return ""
		}
		if len(p) <= n {
			return string(runes)
		}
		runes = runes[:len(runes)-1]
	}
	return ""
}

// Big5Len is the length of s on the wire, or -1 when Big5 cannot encode it.
func Big5Len(s string) int {
	p, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(s))
	if err != nil {
		return -1
	}
	return len(p)
}
