// v148 hired merchant (CEntrustedShopDlg / CEmployeePool / CStoreBankDlg)
// layouts, read from TWMS_v148.1_U_DEVM. Addresses name the client decoder.
package protocol

import (
	"maple-go-server/internal/domain"
)

const (
	RecvEntrustedShopCheck uint16 = 0x6e // permit double-click (0x9D2162)
	RecvStoreBank          uint16 = 0x6f // Fredrick dialog (CStoreBankDlg)

	SendEntrustedShopCheck uint16 = 0x41  // CWvsContext 0x9DC17C
	SendEmployeeEnter      uint16 = 0x1dd // CEmployeePool::OnPacket 0x4EFF4B
	SendEmployeeLeave      uint16 = 0x1de
	SendEmployeeBalloon    uint16 = 0x1e0
	SendStoreBankResult    uint16 = 0x247 // CStoreBankDlg::OnPacket 0x729688
	SendStoreBank          uint16 = 0x248
)

// Mini room type and capacity of an entrusted shop (owner seat + 5 visitors).
const (
	MiniRoomEntrustedShop byte = 5
	MerchantSeats              = 6
	// MerchantRoomBase keeps mini room serials apart from trade references,
	// which share the native enter request (action 9).
	MerchantRoomBase uint32 = 0x40000000
)

// Entrusted shop check results (SendEntrustedShopCheck).
const (
	MerchantCheckOpenTitle byte = 7  // ask for the shop title, then create
	MerchantCheckElsewhere byte = 8  // already open in channel/room
	MerchantCheckFredrick  byte = 9  // collect from Fredrick first
	MerchantCheckOtherChar byte = 10 // another character uses the store bank
	MerchantCheckUnable    byte = 11 // cannot open a shop now
)

// Mini room enter failures (SendPlayerInteraction 10,0,code; 0x5F42EE).
const (
	MiniRoomClosed      byte = 1
	MiniRoomFull        byte = 2
	MiniRoomBusy        byte = 3
	MiniRoomNotAllowed  byte = 6
	MiniRoomNearPortal  byte = 10
	MiniRoomNoShopHere  byte = 13
	MiniRoomFreeMarket  byte = 15
	MiniRoomCannotEnter byte = 17
	MiniRoomMaintenance byte = 18
)

// Reasons a visitor's own dialog is closed (action 18; 0x4F4FC4).
const (
	MerchantLeaveMaintenance byte = 18 // owner is arranging items
	MerchantLeaveExpired     byte = 19 // business hours are over
	MerchantLeaveClosed      byte = 21 // the shop has closed
)

// Buy results (action 43; 0x660FC7).
const (
	MerchantBuyOK        byte = 0
	MerchantBuyNoMesos   byte = 2
	MerchantBuyTooDear   byte = 3
	MerchantBuySellerMax byte = 4
	MerchantBuyNoSpace   byte = 5
	MerchantBuyFailed    byte = 13 // unknown error text
)

// Withdraw-all results (action 32; 0x4F5175).
const (
	MerchantWithdrawOK      byte = 0
	MerchantWithdrawMesos   byte = 1 // too much money; see Fredrick
	MerchantWithdrawNoSpace byte = 3 // inventory full; see Fredrick
	MerchantWithdrawFailed  byte = 4
)

// Store bank results (SendStoreBankResult).
const (
	StoreBankTaken    byte = 32
	StoreBankMesos    byte = 33
	StoreBankUnique   byte = 34
	StoreBankFeeShort byte = 35
	StoreBankNoSpace  byte = 36
	storeBankOpen     byte = 38
	storeBankFee      byte = 39
	storeBankLocation byte = 40
	storeBankUnable   byte = 41
)

// MerchantCheck answers the permit double-click.
func MerchantCheck(mode byte) []byte {
	w := NewWriter(SendEntrustedShopCheck)
	w.U8(mode)
	return w.Packet()
}

// MerchantCheckOpenAt tells the owner where the shop already stands.
func MerchantCheckOpenAt(mapID, channel int) []byte {
	w := NewWriter(SendEntrustedShopCheck)
	w.U8(MerchantCheckElsewhere)
	w.I32(int32(mapID))
	w.U8(byte(channel - 1))
	return w.Packet()
}

// writeMerchantMoney writes one meso field of a merchant or store bank
// packet: a price, a sale total or the shop income. Under the long money
// contract (ClientFeatureLongMerchant) each is 8 bytes. Otherwise the native
// 4-byte field is kept, and an income above it is shown as 2,147,483,647.
func writeMerchantMoney(w *Writer, mesos int64) {
	if mesos < 0 {
		mesos = 0
	}
	if domain.LongMoneyEnabled() {
		w.I64(mesos)
		return
	}
	if mesos > 2147483647 {
		mesos = 2147483647
	}
	w.I32(int32(mesos))
}

func addMiniRoomBalloon(w *Writer, m domain.MerchantShop, serial uint32, users int) {
	w.U8(MiniRoomEntrustedShop)
	w.U32(serial)
	w.String(m.Title)
	w.U8(byte(m.PermitItemID % 10)) // stand appearance
	w.U8(byte(users))
	w.U8(MerchantSeats)
}

// EmployeeEnter spawns the merchant on a map (0x4EFEC6, 0x4EF265, 0x4EE89C).
func EmployeeEnter(m domain.MerchantShop, serial uint32, users int) []byte {
	w := NewWriter(SendEmployeeEnter)
	w.U32(uint32(m.CharacterID))
	w.I32(int32(m.PermitItemID))
	w.I16(int16(m.X))
	w.I16(int16(m.Y))
	w.I16(int16(m.Foothold))
	w.String(m.OwnerName)
	addMiniRoomBalloon(w, m, serial, users)
	return w.Packet()
}

func EmployeeLeave(ownerID int64) []byte {
	w := NewWriter(SendEmployeeLeave)
	w.U32(uint32(ownerID))
	return w.Packet()
}

// EmployeeBalloon refreshes the sign above the merchant (0x4EF7EF).
func EmployeeBalloon(m domain.MerchantShop, serial uint32, users int) []byte {
	w := NewWriter(SendEmployeeBalloon)
	w.U32(uint32(m.CharacterID))
	addMiniRoomBalloon(w, m, serial, users)
	return w.Packet()
}

func addMerchantItem(w *Writer, item domain.MerchantItem) bool {
	w.U16(uint16(item.Bundles))
	w.U16(uint16(item.PerBundle))
	writeMerchantMoney(w, item.Price)
	if item.Equipment != nil {
		addEquipment(w, *item.Equipment)
		return true
	}
	if item.Item.TemplateID <= 0 {
		return false
	}
	bundle := item.Item
	bundle.Quantity = item.PerBundle
	addBundleItem(w, bundle)
	return true
}

// addMerchantItems is the entrusted list refresh (0x4F36F0 + 0x65F780).
func addMerchantItems(w *Writer, m domain.MerchantShop) bool {
	writeMerchantMoney(w, m.Mesos)
	w.U8(byte(len(m.Items)))
	for _, item := range m.Items {
		if !addMerchantItem(w, item) {
			return false
		}
	}
	w.U8(0) // second (wish) list
	w.U8(0)
	return true
}

// MerchantVisitor is one occupied visitor seat (1..5).
type MerchantVisitor struct {
	Seat      byte
	Character domain.Character
}

// MerchantChatLine is a line of the shop's chat history.
type MerchantChatLine struct {
	Text string
	Seat byte
}

// MerchantRoom opens the shop dialog (0x5F3779 then 0x4F5935). seat 0 is the
// owner; the owner also receives the sold list and the uncollected income.
func MerchantRoom(m domain.MerchantShop, seat byte, visitors []MerchantVisitor, chat []MerchantChatLine, firstTime bool, elapsedSeconds int) []byte {
	if len(m.Items) > 255 || len(m.Sales) > 255 || len(chat) > 0xffff {
		return nil
	}
	w := NewWriter(SendPlayerInteraction)
	w.U8(10)
	w.U8(MiniRoomEntrustedShop)
	w.U8(MerchantSeats)
	w.U8(seat)
	w.U8(0) // the employee holds seat 0
	w.I32(int32(m.PermitItemID))
	w.String("精靈商人")
	for _, v := range visitors {
		w.U8(v.Seat)
		addCharLookMode(w, v.Character, false)
		w.String(v.Character.Name)
		w.U16(uint16(v.Character.Job))
	}
	w.U8(0xff)
	w.U16(uint16(len(chat)))
	for _, line := range chat {
		w.String(line.Text)
		w.U8(line.Seat)
	}
	w.String(m.OwnerName)
	if seat == 0 {
		w.I32(int32(elapsedSeconds))
		w.Bool(firstTime)
		w.U8(byte(len(m.Sales)))
		for _, sale := range m.Sales {
			w.I32(int32(sale.TemplateID))
			w.U16(uint16(min(sale.Bundles*sale.PerBundle, 0xffff)))
			writeMerchantMoney(w, sale.Total)
			w.String(sale.Buyer)
		}
		w.I64(m.Mesos)
	}
	w.String(m.Title)
	w.U8(domain.MerchantItemLimit)
	if !addMerchantItems(w, m) {
		return nil
	}
	return w.Packet()
}

// MerchantEnterFailed refuses a create or enter request (no dialog open).
func MerchantEnterFailed(code byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(10)
	w.U8(0)
	w.U8(code)
	w.String("")
	return w.Packet()
}

func MerchantItems(m domain.MerchantShop) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(46)
	if !addMerchantItems(w, m) {
		return nil
	}
	return w.Packet()
}

func MerchantVisitorEnter(v MerchantVisitor) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(9)
	w.U8(v.Seat)
	addCharLookMode(w, v.Character, false)
	w.String(v.Character.Name)
	w.U16(uint16(v.Character.Job))
	return w.Packet()
}

// MerchantVisitorLeft tells the others that a seat emptied.
func MerchantVisitorLeft(seat byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(18)
	w.U8(seat)
	return w.Packet()
}

// MerchantExpelled closes the dialog of the visitor in seat with a reason.
func MerchantExpelled(seat, reason byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(18)
	w.U8(seat)
	w.U8(reason)
	return w.Packet()
}

func MerchantChat(text string, seat byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(14)
	w.U8(13) // user chat (0x5F49D1)
	w.U8(seat)
	w.String(text)
	return w.Packet()
}

func MerchantBuyResult(code byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(43)
	w.U8(code)
	return w.Packet()
}

// MerchantSold adds one line to the owner's sold list (0x66151B).
func MerchantSold(index int, bundles int, buyer string) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(47)
	w.U8(byte(index))
	w.U16(uint16(bundles))
	w.String(buyer)
	return w.Packet()
}

// MerchantArranged answers "tidy up": the income left in the shop.
func MerchantArranged(mesos int64) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(30)
	writeMerchantMoney(w, mesos)
	return w.Packet()
}

// MerchantMesos answers a meso withdrawal: the income left in the shop.
func MerchantMesos(mesos int64) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(34)
	writeMerchantMoney(w, mesos)
	return w.Packet()
}

func MerchantWithdrawResult(code byte) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(32)
	w.U8(code)
	return w.Packet()
}

// MerchantVisitEntry is one line of the owner's visitor list.
type MerchantVisitEntry struct {
	Name    string
	Seconds int
}

func MerchantVisitList(entries []MerchantVisitEntry) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(36)
	w.U16(uint16(len(entries)))
	for _, e := range entries {
		w.String(e.Name)
		w.I32(int32(e.Seconds))
	}
	return w.Packet()
}

func MerchantBlacklist(names []string) []byte {
	w := NewWriter(SendPlayerInteraction)
	w.U8(37)
	w.U16(uint16(len(names)))
	for _, name := range names {
		w.String(name)
	}
	return w.Packet()
}

// StoreBankOpen shows Fredrick's dialog with the package (0x729650, 0x729406).
func StoreBankOpen(npcID int, m domain.MerchantShop) []byte {
	w := NewWriter(SendStoreBank)
	w.U8(storeBankOpen)
	w.I32(int32(npcID))
	w.U8(domain.MerchantItemLimit)
	w.I64(0x7e) // mesos + every inventory type
	writeMerchantMoney(w, m.Mesos)
	for invType := 1; invType <= 5; invType++ {
		count := 0
		for _, item := range m.Items {
			if item.Item.InvType == invType && item.Bundles > 0 {
				count++
			}
		}
		w.U8(byte(count))
		for _, item := range m.Items {
			if item.Item.InvType != invType || item.Bundles < 1 {
				continue
			}
			if item.Equipment != nil {
				addEquipment(w, *item.Equipment)
				continue
			}
			bundle := item.Item
			bundle.Quantity = item.Bundles * item.PerBundle
			addBundleItem(w, bundle)
		}
	}
	return w.Packet()
}

// StoreBankFee asks the owner to confirm the withdrawal (fee 0: plain confirm).
func StoreBankFee(days int, fee int64) []byte {
	w := NewWriter(SendStoreBank)
	w.U8(storeBankFee)
	w.I32(int32(days))
	writeMerchantMoney(w, fee)
	return w.Packet()
}

// StoreBankEmpty is Fredrick's "nothing to collect" line.
func StoreBankEmpty() []byte {
	w := NewWriter(SendStoreBank)
	w.U8(storeBankLocation)
	w.I32(999999999)
	w.I32(999999999)
	w.U8(0)
	return w.Packet()
}

// StoreBankOpenAt is Fredrick's "your shop is still open at ..." line.
func StoreBankOpenAt(mapID, channel int) []byte {
	w := NewWriter(SendStoreBank)
	w.U8(storeBankLocation)
	w.I32(0)
	w.I32(int32(mapID))
	w.U8(byte(channel - 1))
	return w.Packet()
}

func StoreBankUnable() []byte {
	w := NewWriter(SendStoreBank)
	w.U8(storeBankUnable)
	return w.Packet()
}

func StoreBankResult(code byte) []byte {
	w := NewWriter(SendStoreBankResult)
	w.U8(code)
	return w.Packet()
}

// MerchantAction is one client mini room request routed to a hired merchant.
type MerchantAction struct {
	Action    byte
	RoomType  byte   // 6
	Title     string // 6
	Slot      int    // 6 permit slot; 21 inventory slot
	ItemID    int    // 6 permit template
	Serial    uint32 // 9
	Text      string // 14 chat, 38/39 blacklist name
	InvType   int    // 21
	Bundles   int    // 21, 22
	PerBundle int    // 21
	Price     int64  // 21
	Index     int    // 22, 28
}

// PeekMiniRoom reads the action of a player interaction request, and for a
// create or enter request the room type or serial, without consuming them.
func PeekMiniRoom(data []byte) (action byte, roomType byte, serial uint32) {
	if len(data) < 1 {
		return 0xff, 0, 0
	}
	action = data[0]
	switch {
	case action == 6 && len(data) >= 2:
		roomType = data[1]
	case action == 9 && len(data) >= 5:
		serial = uint32(data[1]) | uint32(data[2])<<8 | uint32(data[3])<<16 | uint32(data[4])<<24
	}
	return action, roomType, serial
}

// ReadMerchantAction decodes the entrusted shop requests (COutPacket 0xDA).
// Unknown actions return ErrUnsupportedTradeAction; they are ignored.
func ReadMerchantAction(r *Reader) (MerchantAction, error) {
	v := MerchantAction{Action: r.U8()}
	switch v.Action {
	case 6: // 0x9D7D31: type, title, private flag, permit slot, permit id
		v.RoomType = r.U8()
		v.Title = r.String()
		if r.U8() != 0 {
			_ = r.String()
		}
		v.Slot = int(r.I16())
		v.ItemID = int(r.I32())
	case 9: // 0x91758B: serial plus two flag bytes
		v.Serial = r.U32()
		r.Skip(r.Remaining())
	case 14: // 0x5F39C3: client tick, text
		r.U32()
		v.Text = r.String()
	case 16: // 0x4F41F5: open the shop
		r.Skip(r.Remaining())
	case 21: // 0x6674CF: put an item on sale
		v.InvType = int(r.U8())
		v.Slot = int(r.I16())
		v.Bundles = int(r.I16())
		v.PerBundle = int(r.I16())
		v.Price = ReadMoney(r) // 8 bytes under the long money contract
	case 22: // 0x66160D: buy; the trailing int is the native item checksum
		v.Index = int(r.U8())
		v.Bundles = int(r.I16())
		r.Skip(r.Remaining())
	case 28: // 0x6620D5: take an item back
		r.U8()
		v.Index = int(r.I16())
	case 18, 29, 30, 31, 33, 36, 37: // leave, resume, tidy, close, mesos, lists
		r.Skip(r.Remaining())
	case 38, 39: // blacklist add / remove
		v.Text = r.String()
	default:
		return v, ErrUnsupportedTradeAction
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return v, ErrMalformed
	}
	return v, nil
}
