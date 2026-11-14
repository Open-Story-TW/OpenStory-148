package world

import (
	"sync"

	"maple-go-server/internal/domain"
)

type scenePublicationGate struct {
	mu   sync.Mutex
	refs int // Holders plus waiters; protected by Server.sceneMu.
}

// lockScenePublication serializes local ownership changes for one character.
// The registry mutex never spans a wait, packet write or database call. Empty
// gates are removed so connection churn cannot retain one lock per historic ID.
// Lock order is optional persistMu -> scene gate -> existing map publication;
// never acquire a live persistMu while retaining a scene gate.
//
// Call sites are deliberately introduced separately from this primitive: every
// claim and dependent field publication must use the same boundary before it
// can protect a transition. Merely locking one old publisher is insufficient.
func (s *Server) lockScenePublication(characterID int64) (func(), error) {
	if characterID <= 0 {
		return nil, domain.ErrInvalidInput
	}
	s.sceneMu.Lock()
	if s.sceneGates == nil {
		s.sceneGates = make(map[int64]*scenePublicationGate)
	}
	gate := s.sceneGates[characterID]
	if gate == nil {
		gate = &scenePublicationGate{}
		s.sceneGates[characterID] = gate
	}
	gate.refs++
	s.sceneMu.Unlock()
	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		s.sceneMu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(s.sceneGates, characterID)
		}
		s.sceneMu.Unlock()
	}, nil
}

// sameSceneOwner compares authenticated identity, not mutable position, HP,
// revision, or lease-expiry time. Validation of a new session belongs to the
// authentication/claim path; this comparison also supports isolated fixtures.
func sameSceneOwner(a, b domain.CharacterSession) bool {
	return a.CharacterID == b.CharacterID && a.AccountID == b.AccountID &&
		a.SessionID == b.SessionID && a.Generation == b.Generation &&
		a.ChannelID == b.ChannelID && a.Lease.WorldID == b.Lease.WorldID &&
		a.Lease.InstanceID == b.Lease.InstanceID && a.Lease.Epoch == b.Lease.Epoch
}

// updateOnlineOwned never installs a missing owner or overwrites a replacement
// session. Scene serialization is additionally required for compound registry
// and packet publication; this CAS alone does not protect a whole map entry.
func (s *Server) updateOnlineOwned(expected, next domain.CharacterSession) bool {
	if expected.CharacterID <= 0 || !sameSceneOwner(expected, next) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.online[expected.CharacterID]
	if !ok || !sameSceneOwner(current, expected) {
		return false
	}
	s.online[expected.CharacterID] = next
	return true
}
