package protocol

import (
	"encoding/binary"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestOpenParcelsRecordLayout(t *testing.T) {
	expires := time.Date(2026, 12, 13, 12, 0, 0, 0, time.UTC)
	box := domain.ParcelBox{Parcels: []domain.Parcel{
		{ID: 77, SenderName: "寄件人", Mesos: 12345, Quick: true, Message: "hi", ExpiresAt: expires, Item: &domain.InventoryItem{InvType: 2, TemplateID: 2000000, Quantity: 3}},
		{ID: 78, SenderName: "B", Mesos: 1, ExpiresAt: expires},
	}}
	p := OpenParcels(box, false)
	if binary.LittleEndian.Uint16(p) != SendParcel || p[2] != parcelOpen || p[3] != 0 || p[4] != 2 {
		t.Fatalf("header % x", p[:5])
	}
	rec := p[5:]
	if binary.LittleEndian.Uint32(rec) != 77 {
		t.Fatalf("id % x", rec[:4])
	}
	name := []byte{0xb1, 0x48, 0xa5, 0xf3, 0xa4, 0x48} // 寄件人 in Big5
	for i, b := range name {
		if rec[4+i] != b {
			t.Fatalf("sender % x", rec[4:19])
		}
	}
	if rec[4+len(name)] != 0 || binary.LittleEndian.Uint32(rec[19:]) != 12345 {
		t.Fatalf("mesos % x", rec[15:23])
	}
	if got := int64(binary.LittleEndian.Uint64(rec[23:])); got != mapleTime(expires.Add(-time.Second)) {
		t.Fatalf("expiry %d", got)
	}
	if binary.LittleEndian.Uint32(rec[31:]) != 1 || string(rec[35:37]) != "hi" || rec[37] != 0 {
		t.Fatalf("quick/message % x", rec[31:40])
	}
	if rec[parcelRecordSize] != 1 || rec[parcelRecordSize+1] != 2 || binary.LittleEndian.Uint32(rec[parcelRecordSize+2:]) != 2000000 {
		t.Fatalf("item % x", rec[parcelRecordSize:parcelRecordSize+8])
	}
	// The second record follows the first item; no item, then the expired count.
	second := -1
	for i := parcelRecordSize; i+4 <= len(rec); i++ {
		if binary.LittleEndian.Uint32(rec[i:]) == 78 && rec[i+4] == 'B' {
			second = i
			break
		}
	}
	if second < 0 || len(rec) != second+parcelRecordSize+2 || rec[second+parcelRecordSize] != 0 || rec[len(rec)-1] != 0 {
		t.Fatalf("second record at %d, total %d", second, len(rec))
	}
}

func TestParcelSmallPackets(t *testing.T) {
	if p := ParcelRemoved(9, true); len(p) != 8 || p[2] != 24 || binary.LittleEndian.Uint32(p[3:]) != 9 || p[7] != 3 {
		t.Fatalf("removed % x", p)
	}
	if p := ParcelRemoved(9, false); p[7] != 4 {
		t.Fatalf("taken % x", p)
	}
	if p := ParcelResult(ParcelSent); len(p) != 3 || p[2] != 19 {
		t.Fatalf("result % x", p)
	}
	if p := ParcelArrived("A", true); len(p) != 7 || p[2] != 26 || p[5] != 'A' || p[6] != 1 {
		t.Fatalf("arrived % x", p)
	}
}

func TestReadParcelSend(t *testing.T) {
	w := NewWriter(RecvParcel)
	w.U8(3)
	w.U8(2)
	w.U16(5)
	w.U16(10)
	w.I32(1000)
	w.String("Bob")
	w.U8(0)
	r := NewReader(w.Packet()[3:])
	q, err := ReadParcelSend(r)
	if err != nil || q.InvType != 2 || q.Slot != 5 || q.Quantity != 10 || q.Mesos != 1000 || q.Recipient != "Bob" || q.Quick {
		t.Fatalf("normal %+v %v", q, err)
	}
	w = NewWriter(RecvParcel)
	w.U8(3)
	w.U8(0)
	w.U16(0)
	w.U16(0)
	w.I32(50)
	w.String("Bob")
	w.U8(1)
	w.String("memo")
	w.I32(4)
	q, err = ReadParcelSend(NewReader(w.Packet()[3:]))
	if err != nil || !q.Quick || q.Message != "memo" || q.CouponSlot != 4 {
		t.Fatalf("quick %+v %v", q, err)
	}
	if _, err = ReadParcelSend(NewReader(append(w.Packet()[3:], 0))); err == nil {
		t.Fatal("trailing byte accepted")
	}
}

func TestClipBig5(t *testing.T) {
	if got := clipBig5("寄件人寄件人寄件人", 15); got != "寄件人寄件人寄" {
		t.Fatalf("clip %q", got)
	}
	if Big5Len("寄件") != 4 || Big5Len("ab") != 2 {
		t.Fatal("Big5Len")
	}
}

func TestClientParcelPacket(t *testing.T) {
	previous := domain.ParcelDelay()
	defer domain.SetParcelDelay(previous)
	domain.SetParcelDelay(90 * time.Minute)
	p := ClientParcelPacket()
	if len(p) != 7 || binary.LittleEndian.Uint16(p) != SendClientParcel || p[2] != 1 || binary.LittleEndian.Uint32(p[3:]) != 90 {
		t.Fatalf("parcel config % x", p)
	}
}
