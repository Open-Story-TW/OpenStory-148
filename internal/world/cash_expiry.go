package world

import (
	"context"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// The first field snapshot must already exclude expired equipment. Sending
// inventory deltas before SetField is invalid because the native inventory
// has not yet been constructed. Preserve live position on channel transfers.
func (s *Server) expireCashBeforeField(ctx context.Context, live *liveCharacter) error {
	repo, ok := s.repo.(domain.CashExpiryRepository)
	if !ok {
		return nil
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	session := live.session
	live.mu.Unlock()
	ctx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	result, err := repo.ExpireCashInventory(ctx, session)
	if err != nil {
		return err
	}
	if len(result.Changes) > 0 {
		s.applyInventoryResult(live, result.Character)
	}
	return nil
}

// Call outside persistMu: expiry is a durable command and serializes itself
// against item use, checkpointing, and service/channel transfers.
func (s *Server) expireCashInventory(ctx context.Context, live *liveCharacter, sink packetSink) error {
	repo, ok := s.repo.(domain.CashExpiryRepository)
	if !ok {
		return nil
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	session := live.session
	live.mu.Unlock()
	result, err := repo.ExpireCashInventory(ctx, session)
	if err != nil || len(result.Changes) == 0 {
		return err
	}
	s.applyInventoryResult(live, result.Character)
	if err = sink.WritePacket(protocol.ModifyInventory(result.Changes)); err != nil {
		return err
	}
	if err = sink.WritePacket(protocol.InventoryVitals(result.Character)); err != nil {
		return err
	}
	s.refreshCharacterLook(live)
	return nil
}
