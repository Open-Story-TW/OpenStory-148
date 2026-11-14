package protocol

import (
	"encoding/binary"
	"testing"

	"maple-go-server/internal/domain"
)

func TestExpeditionRequestsMatchNativeSenders(t *testing.T) {
	w := NewRawWriter()
	w.U8(ExpeditionCreate)
	w.I32(2002)
	if q, err := ReadExpeditionRequest(NewReader(w.Packet())); err != nil || q.Type != 2002 {
		t.Fatalf("create %+v %v", q, err)
	}
	w = NewRawWriter()
	w.U8(ExpeditionReply)
	w.String("Bob")
	w.I32(ExpeditionReplyAccept)
	if q, err := ReadExpeditionRequest(NewReader(w.Packet())); err != nil || q.Name != "Bob" || q.Code != ExpeditionReplyAccept {
		t.Fatalf("reply %+v %v", q, err)
	}
	w = NewRawWriter()
	w.U8(ExpeditionMove)
	w.I32(1)
	w.I32(7)
	if q, err := ReadExpeditionRequest(NewReader(w.Packet())); err != nil || q.PartyIndex != 1 || q.CharID != 7 {
		t.Fatalf("move %+v %v", q, err)
	}
	for _, bad := range [][]byte{{ExpeditionLeave, 0}, {ExpeditionKick, 0, 0, 0, 0}, {ExpeditionMove, 5, 0, 0, 0, 7, 0, 0, 0}, {0x47}} {
		if _, err := ReadExpeditionRequest(NewReader(bad)); err == nil {
			t.Fatalf("accepted %x", bad)
		}
	}
}

func TestExpeditionStatusCarriesFive238ByteParties(t *testing.T) {
	p := domain.PartySnapshot{ID: 9, LeaderID: 11, Members: []domain.PartyMember{{CharacterID: 11, Slot: 0, Name: "Alice", Level: 60, Job: 220, Channel: 1}}}
	got := ExpeditionStatus(2002, 0, []domain.PartySnapshot{p}, true, false)
	if len(got) != 2+1+4+4+5*238 || binary.LittleEndian.Uint16(got) != SendExpedition || got[2] != 73 {
		t.Fatalf("status %d %x", len(got), got[:3])
	}
	b := got[11:]
	if binary.LittleEndian.Uint32(b) != 11 || string(b[24:29]) != "Alice" || binary.LittleEndian.Uint32(b[234:]) != 11 || binary.LittleEndian.Uint32(b[238:]) != 0 {
		t.Fatal("party block layout")
	}
	up := ExpeditionPartyUpdate(0, 1, domain.PartySnapshot{})
	if len(up) != 2+1+4+4+238 || up[2] != 84 || binary.LittleEndian.Uint32(up[7:]) != 1 {
		t.Fatalf("update %x", up[:11])
	}
	inv := NewReader(ExpeditionInvitation(60, 220, 0, "Alice", 2002))
	if inv.U16() != SendExpedition || inv.U8() != 86 || inv.I32() != 60 || inv.I32() != 220 || inv.I32() != 0 || inv.String() != "Alice" || inv.I32() != 2002 || inv.Remaining() != 0 {
		t.Fatal("invitation layout")
	}
}
