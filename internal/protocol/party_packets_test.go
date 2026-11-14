package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"maple-go-server/internal/domain"
)

func TestPartyNativeRequestBoundaries(t *testing.T) {
	cases := []struct {
		raw    string
		action domain.PartyAction
	}{{"01", domain.PartyCreate}, {"0200", domain.PartyLeave}, {"040300426f62", domain.PartyInvite}, {"0507000000", domain.PartyExpel}, {"0608000000", domain.PartyLeader}, {"0801", domain.PartySetting}}
	for _, c := range cases {
		raw, _ := hex.DecodeString(c.raw)
		q, err := ReadPartyRequest(NewReader(raw))
		if err != nil || q.Action != c.action {
			t.Fatalf("%s: %+v %v", c.raw, q, err)
		}
		if _, err = ReadPartyRequest(NewReader(append(append([]byte(nil), raw...), 0))); err == nil {
			t.Fatalf("accepted trailing byte %s", c.raw)
		}
		for n := 0; n < len(raw); n++ {
			if _, err = ReadPartyRequest(NewReader(raw[:n])); err == nil {
				t.Fatalf("accepted truncated %s at%d", c.raw, n)
			}
		}
	}
	for _, bad := range []string{"0201", "0301000000", "0802", "0500000000"} {
		raw, _ := hex.DecodeString(bad)
		if _, err := ReadPartyRequest(NewReader(raw)); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	for _, action := range []byte{26, 27, 28, 29, 30, 31} {
		q, err := ReadPartyReply(NewReader([]byte{action, 7, 0, 0, 0}))
		if err != nil {
			t.Fatal(err)
		}
		want := domain.PartyAction(action)
		switch action {
		case 27:
			want = domain.PartyDeny
		case 31:
			want = domain.PartyAccept
		}
		if q.Action != want || q.PartyID != 7 {
			t.Fatal(q)
		}
		if _, err = ReadPartyReply(NewReader([]byte{action, 7, 0, 0, 0, 0})); err == nil {
			t.Fatal("reply accepted trailing byte")
		}
	}
	for _, action := range []byte{0, 3, 25, 32, 58, 59} {
		if _, err := ReadPartyReply(NewReader([]byte{action, 7, 0, 0, 0})); err == nil {
			t.Fatal("accepted unrelated reply", action)
		}
	}
}

func partyWireFixture() domain.PartySnapshot {
	return domain.PartySnapshot{ID: 7, WorldID: 1, LeaderID: 11, Revision: 1, Members: []domain.PartyMember{{CharacterID: 11, Slot: 0, Name: "Alice", Level: 15, Job: 100, Channel: 2, MapID: 100000000}, {CharacterID: 22, Slot: 4, Name: "Bob", Level: 17, Job: 200, SubJob: 1}}}
}

func TestPartyNative383ByteBlockOffsetsAndSparseSlots(t *testing.T) {
	packet := PartySnapshotPacket(partyWireFixture())
	if len(packet) != 2+1+4+PartyDataSize {
		t.Fatalf("snapshot length %d, native expects 2+1+4+383", len(packet))
	}
	if !bytes.Equal(packet[:7], []byte{0x4d, 0x00, 42, 7, 0, 0, 0}) {
		t.Fatal(packet[:7])
	}
	b := packet[7:]
	u32 := func(at int) uint32 { return binary.LittleEndian.Uint32(b[at:]) }
	if u32(0) != 11 || u32(16) != 22 || u32(4) != 0 || string(b[24:29]) != "Alice" || string(b[84:87]) != "Bob" || u32(114) != 100 || u32(130) != 200 || u32(138) != 0 || u32(154) != 1 || u32(162) != 15 || u32(178) != 17 || u32(186) != 1 || u32(202) != 0xffffffff || u32(234) != 11 || u32(238) != 100000000 || u32(254) != 999999999 || u32(262) != 999999999 || b[382] != 0 {
		t.Fatal("native v148 PARTYDATA layout changed")
	}
	// No Java's extra trailing byte on created. No extra I32 before invite mode.
	created, _ := hex.DecodeString("4d000c07000000ffc99a3bffc99a3b000000000000000000")
	if got := PartyCreated(7); !bytes.Equal(got, created) {
		t.Fatalf("created %x want%x", got, created)
	}
	invite, _ := hex.DecodeString("4d0004070000000300426f620a000000640000000200000000")
	got := PartyInvitationPacket(domain.PartyInvitation{PartyID: 7, InviterName: "Bob", InviterLevel: 10, InviterJob: 100, InviterSubJob: 2})
	if !bytes.Equal(got, invite) {
		t.Fatalf("invite %x", got)
	}
	before := partyWireFixture()
	after := before
	after.Members = after.Members[:1]
	if got := PartyDeparted(before, domain.PartySnapshot{}, 11, false); !bytes.Equal(got, []byte{0x4d, 0x00, 16, 7, 0, 0, 0, 11, 0, 0, 0, 0}) {
		t.Fatalf("disband%x", got)
	}
	if got := PartyDeparted(before, after, 22, true); len(got) != 12+1+2+3+PartyDataSize || got[12] != 1 {
		t.Fatalf("expel%x", got[:min(len(got), 20)])
	}
}

func TestPartySnapshotRejectsInvalidMembership(t *testing.T) {
	for _, change := range []func(*domain.PartySnapshot){func(p *domain.PartySnapshot) { p.Members[1].Slot = 0 }, func(p *domain.PartySnapshot) { p.Members[1].CharacterID = 11 }, func(p *domain.PartySnapshot) { p.LeaderID = 99 }, func(p *domain.PartySnapshot) { p.Members[1].Slot = 6 }, func(p *domain.PartySnapshot) { p.Members[1].Channel = 21 }} {
		p := partyWireFixture()
		change(&p)
		if PartySnapshotPacket(p) != nil {
			t.Fatal("serialized invalid party")
		}
	}
}
