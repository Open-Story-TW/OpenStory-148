package protocol

import (
	"maple-go-server/internal/domain"
	"math"
)

const (
	RecvParty      uint16 = 0xdb
	RecvPartyReply uint16 = 0xdc
	SendParty      uint16 = 0x4d
)

// See Open-Maple docs/server/baseline/party-wire.json: the original leave sender includes its
// mode byte; original invitation acceptance uses C9, not Java's C8/action3.
func ReadPartyRequest(r *Reader) (domain.PartyRequest, error) {
	q := domain.PartyRequest{Action: domain.PartyAction(r.U8())}
	switch q.Action {
	case domain.PartyCreate:
	case domain.PartyLeave:
		if r.U8() != 0 {
			return q, ErrMalformed
		}
	case domain.PartyInvite:
		q.Name = r.String()
		if !domain.ValidBuddyText(q.Name, 13) {
			return q, ErrMalformed
		}
	case domain.PartyExpel, domain.PartyLeader:
		q.TargetID = int64(r.I32())
		if q.TargetID <= 0 {
			return q, ErrMalformed
		}
	case domain.PartySetting:
		v := r.U8()
		if v > 1 {
			return q, ErrMalformed
		}
		q.Setting = v != 0
	default:
		return q, ErrMalformed
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return q, ErrMalformed
	}
	return q, nil
}

func ReadPartyReply(r *Reader) (domain.PartyRequest, error) {
	// The v148 invitation handler (0x9EE805 op 4) answers by itself: 26 shown,
	// 27 refused (lock list or party invitations switched off), 28 busy, 29
	// already invited; the invitation window then sends 31 accept or 30 deny.
	// 27 is a refusal on v148, never an acceptance.
	raw := r.U8()
	q := domain.PartyRequest{Action: domain.PartyAction(raw), PartyID: int64(r.I32())}
	switch raw {
	case 27:
		q.Action = domain.PartyDeny
	case 31:
		q.Action = domain.PartyAccept
	}
	switch q.Action {
	case domain.PartyAcknowledge, domain.PartyAccept, domain.PartyPending, domain.PartyDuplicate, domain.PartyDeny:
	default:
		return q, ErrMalformed
	}
	if q.PartyID <= 0 || r.Err() != nil || r.Remaining() != 0 {
		return q, ErrMalformed
	}
	return q, nil
}

func validPartyID(id int64) bool { return id > 0 && id <= math.MaxInt32 }

// PartyDataSize is the v148 PARTYDATA record (0x4C9A44): a 238-byte member
// block, 24 bytes of field ids, six 20-byte town portals and the setting
// byte. The client keeps it at CWvsContext+16924; offsets below are within it.
const PartyDataSize = 383

// writePartyData encodes the v148 record. Member block (0x4C9A32, 238 bytes):
// ids +0, names 6x15 +24, job +114, subjob +138, level +162, channel +186
// (-1 offline), a per-member byte flag +210 (party op 44), leader +234. Then
// field ids +238 (unknown/offline 999999999), portals +262 (town, field,
// skill, x, y) and the setting byte +382. Unknown maps are never inferred
// from the character's stale checkpoint. Door functionality is not advertised.
func writePartyData(w *Writer, p domain.PartySnapshot) {
	if !validPartyID(p.ID) || !validPartyID(p.LeaderID) || len(p.Members) < 1 || len(p.Members) > domain.PartyCapacity {
		w.err = ErrMalformed
		return
	}
	var slots [domain.PartyCapacity]domain.PartyMember
	ids := map[int64]bool{}
	for _, m := range p.Members {
		if !validPartyID(m.CharacterID) || m.Slot < 0 || m.Slot >= domain.PartyCapacity || slots[m.Slot].CharacterID != 0 || ids[m.CharacterID] || !domain.ValidBuddyText(m.Name, 13) || m.Level < 1 || m.Level > domain.MaxWireLevel() || m.Job < 0 || m.Job > math.MaxInt32 || m.SubJob < 0 || m.SubJob > math.MaxInt16 || m.Channel < 0 || m.Channel > 20 || m.MapID < 0 || m.MapID > math.MaxInt32 {
			w.err = ErrMalformed
			return
		}
		slots[m.Slot] = m
		ids[m.CharacterID] = true
	}
	if !ids[p.LeaderID] {
		w.err = ErrMalformed
		return
	}
	writePartyMembers(w, slots, p.LeaderID)
	for _, m := range slots {
		if m.CharacterID == 0 || m.Channel == 0 {
			w.I32(999999999)
		} else {
			w.I32(int32(m.MapID))
		}
	}
	for range domain.PartyCapacity {
		w.I32(999999999)
		w.I32(999999999)
		w.I32(0)
		w.I32(0)
		w.I32(0)
	}
	w.Bool(p.Setting)
}

// writePartyMembers writes the 238-byte member block, which an expedition
// status (0x4F8202) also carries once per party.
func writePartyMembers(w *Writer, slots [domain.PartyCapacity]domain.PartyMember, leader int64) {
	for _, m := range slots {
		w.I32(int32(m.CharacterID))
	}
	for _, m := range slots {
		w.FixedString(m.Name, 15)
	}
	for _, m := range slots {
		w.I32(int32(m.Job))
	}
	for _, m := range slots {
		w.I32(int32(m.SubJob))
	}
	for _, m := range slots {
		w.I32(int32(m.Level))
	}
	for _, m := range slots {
		w.I32(int32(m.Channel - 1))
	}
	w.Zero(24)
	w.I32(int32(leader))
}

func PartySnapshotPacket(p domain.PartySnapshot) []byte {
	w := NewWriter(SendParty)
	w.U8(42)
	w.I32(int32(p.ID))
	writePartyData(w, p)
	return w.Packet()
}

func PartyCreated(id int64) []byte {
	if !validPartyID(id) {
		return nil
	}
	w := NewWriter(SendParty)
	w.U8(12)
	w.I32(int32(id))
	w.I32(999999999)
	w.I32(999999999)
	w.I32(0)
	w.I16(0)
	w.I16(0)
	w.U8(0)
	return w.Packet()
}

func PartyInvitationPacket(i domain.PartyInvitation) []byte {
	if !validPartyID(i.PartyID) || !domain.ValidBuddyText(i.InviterName, 13) || i.InviterLevel < 1 || i.InviterLevel > domain.MaxWireLevel() || i.InviterJob < 0 {
		return nil
	}
	w := NewWriter(SendParty)
	w.U8(4)
	w.I32(int32(i.PartyID))
	w.String(i.InviterName)
	w.I32(int32(i.InviterLevel))
	w.I32(int32(i.InviterJob))
	w.I32(int32(i.InviterSubJob))
	w.U8(0)
	return w.Packet()
}

func PartyJoined(p domain.PartySnapshot, name string) []byte {
	if !domain.ValidBuddyText(name, 13) {
		return nil
	}
	w := NewWriter(SendParty)
	w.U8(19)
	w.I32(int32(p.ID))
	w.String(name)
	writePartyData(w, p)
	return w.Packet()
}

func PartyDeparted(before, after domain.PartySnapshot, target int64, kicked bool) []byte {
	m, ok := before.Member(target)
	if !ok || !validPartyID(before.ID) {
		return nil
	}
	w := NewWriter(SendParty)
	w.U8(16)
	w.I32(int32(before.ID))
	w.I32(int32(target))
	w.Bool(after.ID != 0)
	if after.ID != 0 {
		w.Bool(kicked)
		w.String(m.Name)
		writePartyData(w, after)
	}
	return w.Packet()
}

func PartyLeaderChanged(id int64) []byte {
	if !validPartyID(id) {
		return nil
	}
	w := NewWriter(SendParty)
	w.U8(35)
	w.I32(int32(id))
	w.U8(0)
	return w.Packet()
}

func PartyInvited(name string) []byte {
	if !domain.ValidBuddyText(name, 13) {
		return nil
	}
	w := NewWriter(SendParty)
	w.U8(26)
	w.String(name)
	return w.Packet()
}

func PartyFailure(code uint8) []byte {
	if code != 20 && code != 21 && code != 22 && code != 23 {
		return nil
	}
	w := NewWriter(SendParty)
	w.U8(code)
	return w.Packet()
}
