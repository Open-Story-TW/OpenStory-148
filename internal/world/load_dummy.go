//go:build loaddummy

package world

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"maple-go-server/internal/domain"
)

// startLoadDummies injects synthetic online entries without opening client
// sockets. Gated by build tag loaddummy and MAPLEGO_LOAD_DUMMY=1.
//
// Dummies use negative character IDs and never call ConsumeTransfer /
// ReleaseCharacter / SaveCharacter, so they must not be mixed with real
// ownership fencing. Next: drive map-local move/combat ticks once map actors exist.
func (s *Server) startLoadDummies(ctx context.Context) {
	if os.Getenv("MAPLEGO_LOAD_DUMMY") != "1" {
		return
	}
	count := 0
	if raw := os.Getenv("MAPLEGO_LOAD_DUMMY_COUNT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 5000 {
			slog.Warn("load_dummy_bad_count", "value", raw)
			return
		}
		count = n
	}
	if count == 0 {
		count = 10
	}
	channel := 1
	mapID := 10000
	now := time.Now()
	s.mu.Lock()
	for i := 1; i <= count; i++ {
		id := -int64(i)
		s.online[id] = domain.CharacterSession{
			AccountID:   id,
			CharacterID: id,
			SessionID:   "load-dummy",
			Generation:  1,
			ChannelID:   channel,
			Lease:       s.lease,
			Character: domain.Character{
				ID: id, AccountID: id, WorldID: s.cfg.WorldID,
				Name: "Dummy", Level: 1, MapID: mapID, HP: 50, MaxHP: 50, MP: 50, MaxMP: 50,
				STR: 4, DEX: 4, INT: 4, LUK: 4, Revision: 1,
			},
		}
	}
	online := len(s.online)
	s.mu.Unlock()
	slog.Warn("load_dummy_injected",
		"world", s.cfg.WorldID,
		"count", count,
		"channel", channel,
		"map", mapID,
		"online", online,
		"note", "negative IDs; no DB ownership; sockets skipped",
		"at", now.UTC().Format(time.RFC3339))
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		for i := 1; i <= count; i++ {
			delete(s.online, -int64(i))
		}
		s.mu.Unlock()
	}()
}
