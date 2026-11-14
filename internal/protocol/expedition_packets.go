package protocol

import (
	"maple-go-server/internal/domain"
)

// Expedition (遠征隊) on v148. Requests come in as RecvExpedition with the
// mode byte the expedition tab's senders write (0x4F7316 create .. 0x4F7255
// move); answers go out as SendExpedition and ExpeditionIntermediary::OnPacket
// (0x4F8202, reached from CWvsContext::OnPacket case 0x51) decodes them.
const (
	RecvExpedition uint16 = 0xDE
	SendExpedition uint16 = 0x51
)

// Request modes.
const (
	ExpeditionCreate      uint8 = 0x3F // i32 type
	ExpeditionInvite      uint8 = 0x40 // str name
	ExpeditionReply       uint8 = 0x41 // str inviter, i32 code
	ExpeditionLeave       uint8 = 0x42
	ExpeditionKick        uint8 = 0x43 // i32 character
	ExpeditionLeader      uint8 = 0x44 // i32 character
	ExpeditionPartyLeader uint8 = 0x45 // i32 character
	ExpeditionMove        uint8 = 0x46 // i32 party index, i32 character
)

// Reply codes the invited client sends: 4 refusing invitations or locked, 5
// busy, 6 already answering one, 7 shown (automatic), 8 accept, 9 deny.
const (
	ExpeditionReplyRefusing = 4
	ExpeditionReplyBusy     = 5
	ExpeditionReplyPending  = 6
	ExpeditionReplyShown    = 7
	ExpeditionReplyAccept   = 8
	ExpeditionReplyDeny     = 9
)

// Server ops (0x4F8202).
const (
	expeditionStatusSilent  uint8 = 71 // G
	expeditionRemoveSilent  uint8 = 72 // H
	expeditionStatusCreated uint8 = 73 // I: 創立新的遠征隊
	expeditionJoinedName    uint8 = 74 // J: '%s'加入遠征隊。
	expeditionStatusJoined  uint8 = 75 // K: 已加入遠征隊。
	expeditionLeftName      uint8 = 78 // N: '%s'退出遠征隊。
	expeditionRemoveLeft    uint8 = 79 // O: 退出遠征隊。
	expeditionKickedName    uint8 = 80 // P: '%s'被遠征隊強制退出。
	expeditionRemoveKicked  uint8 = 81 // Q: 在遠征隊遭到強制退出。
	expeditionRemoveDisband uint8 = 82 // R: 遠征隊解散。
	expeditionLeaderChanged uint8 = 83 // S: '%s'成為遠征隊長。
	expeditionPartyUpdate   uint8 = 84 // T
	expeditionInvitation    uint8 = 86 // V
	expeditionMessage       uint8 = 87 // W
)

// ExpeditionMessage codes (op 87): 0 not found, 1 GM only, 2 already in a
// party, 3 level, 4 refusing, 5 busy, 6 already invited, 7 invited.
const (
	ExpeditionMsgNotFound      = 0
	ExpeditionMsgInParty       = 2
	ExpeditionMsgLevel         = 3
	ExpeditionMsgRefusing      = 4
	ExpeditionMsgBusy          = 5
	ExpeditionMsgAlreadyInvite = 6
	ExpeditionMsgInvited       = 7
)

// ExpeditionMaxParties is the number of 238-byte party blocks the status
// packet carries (0x659581 decodes 0x4A6 bytes).
const ExpeditionMaxParties = 5

type ExpeditionRequest struct {
	Mode       uint8
	Type       int
	Name       string
	Code       int
	CharID     int64
	PartyIndex int
}

func ReadExpeditionRequest(r *Reader) (ExpeditionRequest, error) {
	q := ExpeditionRequest{Mode: r.U8()}
	switch q.Mode {
	case ExpeditionCreate:
		q.Type = int(r.I32())
	case ExpeditionInvite:
		q.Name = r.String()
		if !domain.ValidBuddyText(q.Name, 13) {
			return q, ErrMalformed
		}
	case ExpeditionReply:
		q.Name = r.String()
		q.Code = int(r.I32())
		if !domain.ValidBuddyText(q.Name, 13) || q.Code < ExpeditionReplyRefusing || q.Code > ExpeditionReplyDeny {
			return q, ErrMalformed
		}
	case ExpeditionLeave:
	case ExpeditionKick, ExpeditionLeader, ExpeditionPartyLeader:
		q.CharID = int64(r.I32())
		if q.CharID <= 0 {
			return q, ErrMalformed
		}
	case ExpeditionMove:
		q.PartyIndex = int(r.I32())
		q.CharID = int64(r.I32())
		if q.CharID <= 0 || q.PartyIndex < 0 || q.PartyIndex >= ExpeditionMaxParties {
			return q, ErrMalformed
		}
	default:
		return q, ErrMalformed
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return q, ErrMalformed
	}
	return q, nil
}

// writeExpeditionParty writes one 238-byte member block, or zeros for an
// empty index.
func writeExpeditionParty(w *Writer, p domain.PartySnapshot) {
	if p.ID == 0 || len(p.Members) == 0 {
		w.Zero(238)
		return
	}
	var slots [domain.PartyCapacity]domain.PartyMember
	for _, m := range p.Members {
		if m.Slot < 0 || m.Slot >= domain.PartyCapacity || !domain.ValidBuddyText(m.Name, 13) {
			w.err = ErrMalformed
			return
		}
		slots[m.Slot] = m
	}
	writePartyMembers(w, slots, p.LeaderID)
}

// The second field is the index of the party the expedition leader leads;
// the client names the leader from that party block (0x659A53).
func expeditionStatus(op uint8, kind int, leaderIndex int, parties []domain.PartySnapshot) []byte {
	if len(parties) > ExpeditionMaxParties {
		return nil
	}
	w := NewWriter(SendExpedition)
	w.U8(op)
	w.I32(int32(kind))
	w.I32(int32(leaderIndex))
	for i := range ExpeditionMaxParties {
		if i < len(parties) {
			writeExpeditionParty(w, parties[i])
		} else {
			w.Zero(238)
		}
	}
	return w.Packet()
}

// ExpeditionStatus is the full expedition: silent refresh, created or joined.
func ExpeditionStatus(kind int, leaderIndex int, parties []domain.PartySnapshot, created, joined bool) []byte {
	op := expeditionStatusSilent
	if created {
		op = expeditionStatusCreated
	} else if joined {
		op = expeditionStatusJoined
	}
	return expeditionStatus(op, kind, leaderIndex, parties)
}

// ExpeditionRemoved drops the expedition on a client: silent, left, kicked or
// disbanded.
func ExpeditionRemoved(reason string) []byte {
	op := expeditionRemoveSilent
	switch reason {
	case "left":
		op = expeditionRemoveLeft
	case "kicked":
		op = expeditionRemoveKicked
	case "disbanded":
		op = expeditionRemoveDisband
	}
	w := NewWriter(SendExpedition)
	w.U8(op)
	return w.Packet()
}

func expeditionName(op uint8, name string) []byte {
	if !domain.ValidBuddyText(name, 13) {
		return nil
	}
	w := NewWriter(SendExpedition)
	w.U8(op)
	w.String(name)
	return w.Packet()
}

func ExpeditionJoined(name string) []byte { return expeditionName(expeditionJoinedName, name) }
func ExpeditionLeft(name string) []byte   { return expeditionName(expeditionLeftName, name) }
func ExpeditionKicked(name string) []byte { return expeditionName(expeditionKickedName, name) }

// ExpeditionLeaderChanged names the party index whose leader now leads.
func ExpeditionLeaderChanged(leaderIndex int) []byte {
	w := NewWriter(SendExpedition)
	w.U8(expeditionLeaderChanged)
	w.I32(int32(leaderIndex))
	return w.Packet()
}

// ExpeditionPartyUpdate replaces party index. An empty party removes that
// index and shifts the later ones down (0x4F7AD4).
func ExpeditionPartyUpdate(leaderIndex int, index int, p domain.PartySnapshot) []byte {
	if index < 0 || index >= ExpeditionMaxParties {
		return nil
	}
	w := NewWriter(SendExpedition)
	w.U8(expeditionPartyUpdate)
	w.I32(int32(leaderIndex))
	w.I32(int32(index))
	writeExpeditionParty(w, p)
	return w.Packet()
}

// ExpeditionInvitation is decoded by 0x4F6F42: level, job, subjob, inviter
// name, type. The invited client answers with a reply code itself.
func ExpeditionInvitation(level, job, subJob int, inviter string, kind int) []byte {
	if !domain.ValidBuddyText(inviter, 13) {
		return nil
	}
	w := NewWriter(SendExpedition)
	w.U8(expeditionInvitation)
	w.I32(int32(level))
	w.I32(int32(job))
	w.I32(int32(subJob))
	w.String(inviter)
	w.I32(int32(kind))
	return w.Packet()
}

// ExpeditionMessage shows one of the inviter notices (0x4F7D4E).
func ExpeditionMessage(code int, name string) []byte {
	w := NewWriter(SendExpedition)
	w.U8(expeditionMessage)
	w.I32(int32(code))
	w.String(name)
	return w.Packet()
}
