package world

import (
	"sort"
	"time"

	"maple-go-server/internal/protocol"
)

// liveOwnerSummonCount returns the number of active non-expired summons of
// skillID currently owned by charID in (channel, mapID).
func (s *Server) liveOwnerSummonCount(channel, mapID int, charID int64, skillID int, now time.Time) int {
	if s.gameplay == nil {
		return 0
	}
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	room := s.gameplay.rooms[mapKey{channel: channel, mapID: mapID}]
	if room == nil {
		return 0
	}
	count := 0
	for _, m := range room.summons {
		if m.source == skillID && m.ownerID == charID && m.expiresAt.After(now) {
			count++
		}
	}
	return count
}

// teslaCoil is Magnetic Field (35111002). Three coils of one owner form a
// triangle. The v148 client reads the triangle only when a coil is created
// (0x738AE3): a coil created with state 1 carries the three corners and runs
// the triangle attack itself (0x738C60 -> 0x731E23 every native interval),
// state 2 marks the other two corners, and both use action 0x2A.
const teslaCoil = 35111002

func ownerCoilsLocked(room *gameplayRoom, m *roomSummon) []*roomSummon {
	var coils []*roomSummon
	for _, c := range room.summons {
		if c.source == teslaCoil && c.ownerEntry == m.ownerEntry && c.ownerID == m.ownerID {
			coils = append(coils, c)
		}
	}
	sort.Slice(coils, func(i, j int) bool { return coils[i].objectID < coils[j].objectID })
	return coils
}

// formTeslaTriangleLocked runs after the new coil m was stored. With three
// coils up, the two older ones are created again as corners (state 2) and m
// becomes the triangle coil (state 1); a coil beyond three ends the oldest.
func formTeslaTriangleLocked(room *gameplayRoom, m *roomSummon) []roomDelivery {
	coils := ownerCoilsLocked(room, m)
	var out []roomDelivery
	for len(coils) > 3 {
		old := coils[0]
		coils = coils[1:]
		delete(room.summons, old.objectID)
		out = append(out, old.removePackets(room, protocol.SummonLeaveNone)...)
	}
	if len(coils) != 3 {
		return out
	}
	for i, c := range coils {
		m.teslaPoints[i] = [2]int16{int16(c.x), int16(c.y)}
	}
	for _, c := range coils {
		if c == m {
			continue
		}
		out = append(out, c.removePackets(room, protocol.SummonLeaveNone)...)
		c.teslaState, c.teslaPoints = 2, [3][2]int16{}
		out = append(out, roomBroadcast(room, protocol.SpawnSummon(c.view(false)), -1)...)
	}
	m.teslaState = 1
	return out
}

// teslaTrianglePacketLocked returns the 0x110 CUser::OnTeslaTriangle packet
// linking the leader coil m (coils[0]) with its two corner coils after m's
// SpawnSummon packet has been emitted.
func teslaTrianglePacketLocked(room *gameplayRoom, m *roomSummon) []byte {
	if m == nil || m.source != teslaCoil || m.teslaState != 1 {
		return nil
	}
	var ids [3]int32
	ids[0] = m.objectID
	n := 1
	for _, c := range ownerCoilsLocked(room, m) {
		if c == m {
			continue
		}
		if n < 3 {
			ids[n] = c.objectID
			n++
		}
	}
	if n != 3 {
		return nil
	}
	return protocol.TeslaTriangle(m.ownerID, ids)
}

// breakTeslaTriangleLocked ends the rest of a triangle when one of its coils
// leaves: the triangle coil would keep attacking an area with a missing corner.
func breakTeslaTriangleLocked(room *gameplayRoom, gone *roomSummon) []roomDelivery {
	if gone.source != teslaCoil || gone.teslaState == 0 {
		return nil
	}
	var out []roomDelivery
	for _, c := range ownerCoilsLocked(room, gone) {
		if c.teslaState != 0 {
			delete(room.summons, c.objectID)
			out = append(out, c.removePackets(room, protocol.SummonLeaveNone)...)
		}
	}
	return out
}
