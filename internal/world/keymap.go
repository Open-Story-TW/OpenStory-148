package world

import (
	"context"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) keymapPacket(ctx context.Context, session domain.CharacterSession) ([]byte, error) {
	repo, ok := s.repo.(domain.KeymapRepository)
	if !ok {
		return protocol.DefaultKeymap(), nil
	}
	session.Lease = s.currentLease()
	ctx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	mapping, err := repo.ReadKeymap(ctx, session)
	if err != nil {
		return nil, err
	}
	return protocol.KeymapPacket(mapping), nil
}

func (s *Server) changeKeymap(ctx context.Context, live *liveCharacter, r *protocol.Reader) error {
	changes, err := protocol.ReadKeymapChanges(r)
	if err != nil {
		return nil
	}
	repo, ok := s.repo.(domain.KeymapRepository)
	if !ok {
		return nil
	}
	live.mu.Lock()
	session := live.session
	session.Lease = s.currentLease()
	live.mu.Unlock()
	ctx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	return repo.UpdateKeymap(ctx, session, changes)
}
