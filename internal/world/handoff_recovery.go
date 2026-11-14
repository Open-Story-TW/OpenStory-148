package world

import (
	"context"
	"log/slog"
	"time"

	"maple-go-server/internal/domain"
)

// A claim commits ownership before its socket installs the scene, so a session
// must stay unclaimed this long before it is treated as lost.
const orphanSessionGrace = 30 * time.Second

type orphanKey struct {
	accountID  int64
	sessionID  string
	generation int64
}

// reclaimOrphanSessions releases durable world sessions owned by this lease
// that no live socket or pending channel transfer holds. They appear when a
// claim commits but its reply is lost, or a disconnect release fails; without
// this the account stays "online" until the whole world lease is lost.
func (s *Server) reclaimOrphanSessions(ctx context.Context, now time.Time) {
	repo, ok := s.repo.(domain.HandoffRecoveryRepository)
	if !ok {
		return
	}
	lease := s.currentLease()
	owned, err := repo.ListWorldSessions(ctx, lease)
	if err != nil {
		slog.Warn("world_orphan_scan_failed", "world", lease.WorldID, "error", err)
		return
	}
	var release []domain.CharacterSession
	s.mu.Lock()
	if s.orphanSince == nil {
		s.orphanSince = make(map[orphanKey]time.Time)
	}
	seen := make(map[orphanKey]bool, len(owned))
	for _, o := range owned {
		if s.holdsSessionLocked(o) {
			continue
		}
		key := orphanKey{o.AccountID, o.SessionID, o.Generation}
		seen[key] = true
		first, tracked := s.orphanSince[key]
		if !tracked {
			s.orphanSince[key] = now
			continue
		}
		if now.Sub(first) >= orphanSessionGrace {
			delete(s.orphanSince, key)
			release = append(release, domain.CharacterSession{AccountID: o.AccountID, CharacterID: o.CharacterID, SessionID: o.SessionID, Generation: o.Generation, ChannelID: o.ChannelID, Lease: lease})
		}
	}
	for key := range s.orphanSince {
		if !seen[key] {
			delete(s.orphanSince, key)
		}
	}
	s.mu.Unlock()
	for _, session := range release {
		// ReleaseCharacter is fenced by generation, so a newer claim survives.
		if err = s.repo.ReleaseCharacter(ctx, session); err != nil {
			slog.Warn("world_orphan_release_failed", "character", session.CharacterID, "error", err)
			continue
		}
		slog.Warn("world_orphan_session_released", "world", lease.WorldID, "character", session.CharacterID, "generation", session.Generation)
	}
}

func (s *Server) holdsSessionLocked(o domain.OwnedSession) bool {
	if current, ok := s.online[o.CharacterID]; ok && current.SessionID == o.SessionID && current.Generation == o.Generation {
		return true
	}
	for _, pending := range s.channelTransfers {
		// An uncertain Finish may already be durable as the next generation;
		// its own expiry path settles it.
		if pending.session.CharacterID == o.CharacterID && pending.session.SessionID == o.SessionID {
			return true
		}
	}
	return false
}
