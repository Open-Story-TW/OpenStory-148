package world

import "maple-go-server/internal/domain"

// A source socket may still be cleaning its registries after a transfer has
// expired and a new login owns this character. Delete only its own generation.
func (s *Server) removeOnlineSession(expected domain.CharacterSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.online[expected.CharacterID]
	if ok && current.AccountID == expected.AccountID && current.SessionID == expected.SessionID && current.Generation == expected.Generation && current.Lease.InstanceID == expected.Lease.InstanceID && current.Lease.Epoch == expected.Lease.Epoch {
		delete(s.online, expected.CharacterID)
	}
}
