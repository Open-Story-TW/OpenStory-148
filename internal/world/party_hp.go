package world

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type partyHPState struct{ hp, maxHP, channel, mapID int }

// Must be called after map.Enter/Move has released the map lock. Native0x130
// addresses an already-spawned remote user, so AOI reentry invalidates the
// recipient cache even if HP itself did not change while out of sight.
func (s *Server) partyHPVisible(channel, field int, source, target int64) bool {
	if s.maps == nil {
		return false
	}
	s.maps.mu.Lock()
	defer s.maps.mu.Unlock()
	room := s.maps.rooms[mapKey{channel: channel, mapID: field}]
	a, b := room[source], room[target]
	if a == nil || b == nil {
		return false
	}
	_, ab := a.visible[target]
	_, ba := b.visible[source]
	return ab && ba
}

// Caller holds partySpace.mu. Runtime snapshots, never saved character HP,
// determine eligible updates; no database operation or socket write occurs.
func (s *Server) partyHPDeltas(p domain.PartySnapshot) []partyDelivery {
	var out []partyDelivery
	for _, target := range p.Members {
		peer := s.parties.peers[target.CharacterID]
		if peer == nil || peer.party.ID != p.ID {
			continue
		}
		if peer.hpSeen == nil {
			peer.hpSeen = map[int64]partyHPState{}
		}
		next := map[int64]partyHPState{}
		var packets [][]byte
		for _, source := range p.Members {
			if source.CharacterID == target.CharacterID || target.Channel == 0 || source.Channel != target.Channel || source.MapID != target.MapID {
				continue
			}
			other := s.parties.peers[source.CharacterID]
			if other == nil || other.party.ID != p.ID || !s.partyHPVisible(source.Channel, source.MapID, source.CharacterID, target.CharacterID) {
				continue
			}
			packet := protocol.PartyMemberHP(source.CharacterID, source.HP, source.MaxHP)
			if packet == nil {
				continue
			}
			state := partyHPState{source.HP, source.MaxHP, source.Channel, source.MapID}
			next[source.CharacterID] = state
			if old, known := peer.hpSeen[source.CharacterID]; !known || old != state {
				packets = append(packets, packet)
			}
		}
		peer.hpSeen = next
		if len(packets) > 0 {
			out = append(out, partyDelivery{peer: peer, packets: packets})
		}
	}
	return out
}
