package world

import (
	"context"
	"log/slog"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Materialization is retryable: the kill transaction has already committed a
// pinned loot job. A temporary room-capacity or DB failure never rolls back a
// previously acknowledged experience reward or rerolls the loot.
func (s *Server) spawnCombatLoot(ctx context.Context, live *liveCharacter, sink packetSink, operationID string, mobID, x, y int) error {
	repo, ok := s.repo.(domain.CombatLootRepository)
	if !ok {
		return nil
	}
	step, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.materializeCombatLoot(step, repo, operationID)
}

func (s *Server) materializeCombatLoot(ctx context.Context, repo domain.CombatLootRepository, operationID string) error {
	drops, err := repo.MaterializeCombatLoot(ctx, s.currentLease(), operationID)
	if err != nil {
		slog.Warn("combat_loot_pending", "world", s.cfg.WorldID, "operation", operationID, "error", err)
		// The worker retries the durable job. Never pretend the committed kill
		// failed, which would otherwise invite another attempt at its reward.
		return nil
	}
	for _, drop := range drops {
		s.broadcastCombatDrop(drop)
	}
	return nil
}

func (s *Server) runCombatLoot(ctx context.Context) {
	repo, ok := s.repo.(domain.CombatLootRepository)
	if !ok {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycle, cancel := context.WithTimeout(ctx, 2*time.Second)
			if lifecycle, ok := s.repo.(domain.DropLifecycleRepository); ok {
				expired, expireErr := lifecycle.ExpireItemDrops(cycle, s.currentLease(), 256)
				if expireErr != nil {
					slog.Warn("drop_expiry_failed", "world", s.cfg.WorldID, "error", expireErr)
				} else {
					for _, drop := range expired {
						s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
					}
				}
			}
			ids, err := repo.PendingCombatLoot(cycle, s.currentLease(), 32)
			if err == nil {
				for _, id := range ids {
					_ = s.materializeCombatLoot(cycle, repo, id)
				}
			}
			cancel()
		}
	}
}
