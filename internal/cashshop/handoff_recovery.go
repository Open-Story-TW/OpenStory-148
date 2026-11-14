package cashshop

import (
	"context"
	"log/slog"
	"time"

	"maple-go-server/internal/domain"
)

// A claim commits the cashshop session before its socket registers it.
const orphanSessionGrace = 30 * time.Second

type cashSessionKey struct {
	accountID  int64
	sessionID  string
	generation int64
}

func keyOf(c domain.CashSession) cashSessionKey {
	return cashSessionKey{c.Session.AccountID, c.Session.SessionID, c.Session.Generation}
}

func (s *server) trackLive(c domain.CashSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live == nil {
		s.live = make(map[cashSessionKey]int)
	}
	s.live[keyOf(c)]++
}

func (s *server) untrackLive(c domain.CashSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := keyOf(c)
	if s.live[key] <= 1 {
		delete(s.live, key)
		return
	}
	s.live[key]--
}

// reclaimOrphanSessions releases cashshop sessions this lease owns that no
// socket holds: a claim whose reply was lost, or a failed disconnect release.
// Lease renewal keeps those rows alive, so nothing else would ever free them.
func (s *server) reclaimOrphanSessions(ctx context.Context, now time.Time) {
	repo, ok := s.repo.(domain.CashHandoffRecoveryRepository)
	if !ok {
		return
	}
	lease := s.currentLease()
	owned, err := repo.ListCashSessions(ctx, lease)
	if err != nil {
		slog.Warn("cashshop_orphan_scan_failed", "error", err)
		return
	}
	var release []domain.CashSession
	s.mu.Lock()
	if s.orphanSince == nil {
		s.orphanSince = make(map[cashSessionKey]time.Time)
	}
	seen := make(map[cashSessionKey]bool, len(owned))
	for _, o := range owned {
		key := cashSessionKey{o.AccountID, o.SessionID, o.Generation}
		if s.live[key] > 0 {
			continue
		}
		seen[key] = true
		first, tracked := s.orphanSince[key]
		if !tracked {
			s.orphanSince[key] = now
			continue
		}
		if now.Sub(first) >= orphanSessionGrace {
			delete(s.orphanSince, key)
			release = append(release, domain.CashSession{Session: domain.CharacterSession{AccountID: o.AccountID, CharacterID: o.CharacterID, SessionID: o.SessionID, Generation: o.Generation}, Lease: lease})
		}
	}
	for key := range s.orphanSince {
		if !seen[key] {
			delete(s.orphanSince, key)
		}
	}
	s.mu.Unlock()
	for _, c := range release {
		// ReleaseCashCharacter is fenced by generation and lease epoch.
		if err = s.repo.ReleaseCashCharacter(ctx, c); err != nil {
			slog.Warn("cashshop_orphan_release_failed", "character", c.Session.CharacterID, "error", err)
			continue
		}
		slog.Warn("cashshop_orphan_session_released", "character", c.Session.CharacterID, "generation", c.Session.Generation)
	}
}
