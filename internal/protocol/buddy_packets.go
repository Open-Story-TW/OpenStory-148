package protocol

import (
	"math"

	"maple-go-server/internal/domain"
)

const RecvBuddy uint16 = 0xe4
const SendBuddy uint16 = 0x52

// BuddyReload is the request 0x9EDFB4 sends (mode 0, no payload) when the
// friend tab wants its list again; the server answers with a full list.
const BuddyReload uint8 = 0

// The fixed native name and group arrays reserve a terminating NUL.
func ReadBuddyRequest(r *Reader) (domain.BuddyRequest, error) {
	q := domain.BuddyRequest{Action: r.U8()}
	switch q.Action {
	case BuddyReload:
	case 1:
		q.Name, q.Group = r.String(), r.String()
		if !domain.ValidBuddyText(q.Name, 13) || !domain.ValidBuddyText(q.Group, domain.BuddyGroupLimit) {
			return q, ErrMalformed
		}
	case 2, 3:
		q.TargetID = int64(r.I32())
		if q.TargetID <= 0 {
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

// BuddyRecordSize is the v148 friend record (0x4C9AE0): id +0, name +4 (15),
// pending +19, channel +20 (-1 offline), group +24 (33). The friend tab
// (0x8B7F07) reads exactly these fields.
const BuddyRecordSize = 57

func writeBuddyRecord(w *Writer, e domain.BuddyEntry) {
	if e.CharacterID <= 0 || e.CharacterID > math.MaxInt32 || !domain.ValidBuddyText(e.Name, 13) || !domain.ValidBuddyText(e.Group, domain.BuddyGroupLimit) || e.Channel < 0 {
		w.err = ErrMalformed
		return
	}
	w.I32(int32(e.CharacterID))
	w.FixedString(e.Name, 15)
	w.Bool(e.Pending)
	w.I32(int32(e.Channel - 1))
	w.FixedString(e.Group, 33)
}

func BuddyListPacket(list domain.BuddyList, action uint8) []byte {
	if (action != 7 && action != 10 && action != 18) || len(list.Entries) > domain.MaxWireBuddies() {
		return nil
	}
	w := NewWriter(SendBuddy)
	w.U8(action)
	buddyCount(w, len(list.Entries))
	for _, e := range list.Entries {
		writeBuddyRecord(w, e)
	}
	w.Zero(4 * len(list.Entries))
	return w.Packet()
}

// The v148 OnFriendResult (0x9EFC85, action 9) decodes level, job and subjob
// before the 57-byte record (0x9EE340) and its trailing byte.
func BuddyInvitation(id int64, name string, level, job, subJob, channel int) []byte {
	w := NewWriter(SendBuddy)
	w.U8(9)
	w.I32(int32(id))
	w.String(name)
	w.I32(int32(level))
	w.I32(int32(job))
	w.I32(int32(subJob))
	writeBuddyRecord(w, domain.BuddyEntry{CharacterID: id, Name: name, Group: "群組未指定", Pending: true, Channel: channel})
	w.U8(0) // sub_9515E9 ancillary byte, after the complete record.
	return w.Packet()
}

// BuddyCapacity is mode 21; the capacity takes the friend count's width.
func BuddyCapacity(capacity int) []byte {
	if capacity < 1 || capacity > domain.MaxWireBuddies() {
		return nil
	}
	w := NewWriter(SendBuddy)
	w.U8(21)
	buddyCount(w, capacity)
	return w.Packet()
}

// buddyCount writes a friend count or capacity: two bytes under the large
// buddy contract, one byte natively.
func buddyCount(w *Writer, n int) {
	if domain.LargeBuddyEnabled() {
		w.U16(uint16(n))
		return
	}
	w.U8(byte(n))
}

func BuddyStatus(id int64, channel int) []byte {
	if id <= 0 || id > math.MaxInt32 || channel < 0 {
		return nil
	}
	w := NewWriter(SendBuddy)
	w.U8(20)
	w.I32(int32(id))
	w.U8(0)
	w.I32(int32(channel - 1))
	return w.Packet()
}

func BuddyFailure(code uint8) []byte {
	if code < 11 || code > 15 {
		return nil
	}
	w := NewWriter(SendBuddy)
	w.U8(code)
	return w.Packet()
}
