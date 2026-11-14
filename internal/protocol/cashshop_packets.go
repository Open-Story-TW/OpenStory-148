package protocol

import (
	"encoding/hex"
	"sort"
	"time"

	"maple-go-server/internal/domain"
)

const RecvCashUpdate uint16 = 0x1b1
const RecvCashOperation uint16 = 0x1b2

// RecvNoteAction is Legacy NOTE_ACTION. In the cash shop native 47A97B sends
// type 0 as the receiver's thank-you memo to a gift sender.
const RecvNoteAction uint16 = 0xe5
const SendCashOpen uint16 = 0xd3
const SendCashInfo uint16 = 0xd4
const SendCashWallet uint16 = 0x260
const SendCashOperation uint16 = 0x261

func CashOpen(c domain.Character) []byte {
	w := NewWriter(SendCashOpen)
	addCharacterInfo(w, c, time.Now())
	return w.Packet()
}
func CashWalletPacket(wallet domain.CashWallet) []byte {
	w := NewWriter(SendCashWallet)
	writeMoney(w, wallet.Credit)
	writeMoney(w, wallet.Points)
	// Native CCashShop::OnQueryCashResult (0x47D1E4) decodes two int32s.
	return w.Packet()
}
func CashFailure(code byte) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(104)
	w.U8(code)
	return w.Packet()
}
func CashGifts() []byte { w := NewWriter(SendCashOperation); w.U8(97); w.U16(0); return w.Packet() }
func CashWishlist() []byte {
	w := NewWriter(SendCashOperation)
	w.U8(99)
	w.Zero(40)
	return w.Packet()
}

func CashInfo(account string, products []domain.CashProduct) []byte {
	w := NewWriter(SendCashInfo)
	w.String(account)
	w.I32(0)
	items := make([]domain.CashProduct, 0)
	for _, p := range products {
		if p.OnSale {
			items = append(items, p)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SN < items[j].SN })
	if len(items) > 2800 {
		return nil
	}
	w.U16(uint16(len(items)))
	for _, p := range items {
		w.I32(int32(p.SN))
		w.I32(0x627)
		w.I32(int32(p.TemplateID))
		w.U16(uint16(p.Quantity))
		w.I32(int32(p.Price))
		w.U16(uint16(p.PeriodDays))
		w.U8(byte(p.Gender))
		w.U8(1)
	}
	w.U16(0)
	w.U8(0)
	w.I32(0)
	// Fixed v145 sale-info bytes from MTSCSPacket.warpCSInfo; these are not
	// synthetic goods or authority for purchases.
	w.Zero(107)
	best, _ := hex.DecodeString("02000000310000000a00100012000e07e03b8b0b60ce8a0b69006c006c002f0035003300320030003000310031002f00730075006d006d006f006e002f00610074007400610063006b0031002f003100000000000000000002001a0004010807020000003200000005001c0006000807a0012e0058cd8a0b")
	w.Bytes(best)
	for category := 1; category <= 8; category++ {
		for gender := 0; gender <= 1; gender++ {
			for _, sn := range []int32{10003055, 10003090, 10103464, 10002960, 10103363} {
				w.I32(int32(category))
				w.I32(int32(gender))
				w.I32(sn)
			}
		}
	}
	w.U16(0)
	w.U16(0)
	w.U8(0)
	w.U8(0)
	packet := w.Packet()
	if len(packet) > MaxPacketSize {
		return nil
	}
	return packet
}

func cashItemInfo(w *Writer, item domain.CashItem) {
	w.I64(item.ID)
	w.I64(item.AccountID)
	w.I32(int32(item.TemplateID))
	w.I32(int32(item.SN))
	w.U16(uint16(item.Quantity))
	w.FixedString(item.GiftFrom, 13)
	if item.ExpiresAt > 0 {
		w.I64(mapleTime(time.UnixMilli(item.ExpiresAt)))
	} else {
		w.I64(maxTime)
	}
	w.I64(0)
	w.Zero(12)
}
func CashLocker(items []domain.CashItem) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(95)
	w.U16(uint16(len(items)))
	for _, item := range items {
		cashItemInfo(w, item)
	}
	if len(items) > 0 {
		count := 0
		for _, item := range items {
			if item.InvType == 1 {
				count++
			}
		}
		w.I32(int32(count))
		for _, item := range items {
			if item.InvType == 1 && item.Equipment != nil {
				e := *item.Equipment
				e.CashID = item.ID
				e.ExpiresAt = item.ExpiresAt
				addEquipment(w, e)
			}
		}
	}
	w.U16(48)
	w.U16(15)
	w.U16(0)
	w.U16(4)
	return w.Packet()
}
func CashPurchased(item domain.CashItem) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(103)
	cashItemInfo(w, item)
	return w.Packet()
}
func CashFromLocker(item domain.CashItem, c domain.Character) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(122)
	w.U16(uint16(item.Slot))
	if item.InvType == 1 {
		for _, e := range c.Equipment {
			if e.CashID == item.ID {
				addEquipment(w, e)
				break
			}
		}
	} else {
		for _, i := range c.Items {
			if i.CashID == item.ID {
				if pet, ok := petForItem(c.Pets, i); ok {
					addPetItem(w, pet)
				} else {
					addBundleItem(w, i)
				}
				break
			}
		}
	}
	w.I32(0)
	return w.Packet()
}
func CashToLocker(item domain.CashItem) []byte {
	w := NewWriter(SendCashOperation)
	w.U8(124) // 0x47A5DA also removes the matching physical inventory serial.
	cashItemInfo(w, item)
	return w.Packet()
}
