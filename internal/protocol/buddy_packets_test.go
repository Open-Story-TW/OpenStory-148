package protocol

import (
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
)

func TestBuddyNativeRecordAndInvitation(t *testing.T) {
	defer domain.SetLargeBuddyForTest(false)()
	list := domain.BuddyList{Capacity: 232, Entries: []domain.BuddyEntry{{CharacterID: 101, Name: "測試", Group: "朋友", Channel: 2}, {CharacterID: 102, Name: "Other", Group: "Default", Pending: true}}}
	p := BuddyListPacket(list, 7)
	if len(p) != 4+2*BuddyRecordSize+2*4 || binary.LittleEndian.Uint16(p) != 0x52 || p[3] != 2 {
		t.Fatalf("native list layout: %x", p)
	}
	if p[4+19] != 0 || int32(binary.LittleEndian.Uint32(p[4+20:])) != 1 || p[4+57+19] != 1 || int32(binary.LittleEndian.Uint32(p[4+57+20:])) != -1 || string(p[4+24:4+28]) != "\xaa\x42\xa4\xcd" {
		t.Fatalf("flags/channels shifted: %x", p)
	}
	p = BuddyInvitation(101, "Hero", 12, 100, 1, 2)
	r := NewReader(p)
	if r.U16() != 0x52 || r.U8() != 9 || r.I32() != 101 || r.String() != "Hero" || r.I32() != 12 || r.I32() != 100 || r.I32() != 1 {
		t.Fatalf("invitation header: %x", p)
	}
	if r.Remaining() != BuddyRecordSize+1 || r.I32() != 101 {
		t.Fatalf("v148 invitation is subjob then the 57-byte record: %x", p)
	}
	r.Skip(15)
	if r.U8() != 1 || r.I32() != 1 {
		t.Fatal("pending invitation metadata")
	}
	r.Skip(33)
	if r.U8() != 0 || r.Remaining() != 0 || r.Err() != nil {
		t.Fatal("invitation trailer")
	}
	if got := BuddyCapacity(232); len(got) != 4 || got[3] != 232 {
		t.Fatalf("capacity %x", got)
	}
	if got := BuddyStatus(101, 0); len(got) != 12 || int32(binary.LittleEndian.Uint32(got[8:])) != -1 {
		t.Fatalf("offline %x", got)
	}
}

func TestBuddyRequestsRequireExactWholeBodyAndEncodedBounds(t *testing.T) {
	w := NewRawWriter()
	w.U8(1)
	w.String("GoMcpTwo")
	w.String("朋友")
	p := w.Packet()
	q, err := ReadBuddyRequest(NewReader(p))
	if err != nil || q.Name != "GoMcpTwo" || q.Group != "朋友" {
		t.Fatalf("request %+v %v", q, err)
	}
	for i := 0; i < len(p); i++ {
		if _, err := ReadBuddyRequest(NewReader(p[:i])); err == nil {
			t.Fatalf("truncated body %d", i)
		}
	}
	if _, err := ReadBuddyRequest(NewReader(append(p, 0))); err == nil {
		t.Fatal("trailing accepted")
	}
	for _, action := range []uint8{2, 3} {
		w = NewRawWriter()
		w.U8(action)
		w.I32(102)
		if _, err := ReadBuddyRequest(NewReader(w.Packet())); err != nil {
			t.Fatal(err)
		}
	}
	if domain.ValidBuddyText("12345678901234", 13) || domain.ValidBuddyText("一二三四五六七八九十一二三四五六七", domain.BuddyGroupLimit) || domain.ValidBuddyText("bad\x00", 13) || domain.ValidBuddyText("🙂", 13) {
		t.Fatal("invalid fixed encoded text accepted")
	}
}
