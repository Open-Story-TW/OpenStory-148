package world

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type channelTransferKey struct {
	handle  int64
	channel int
}
type channelTransfer struct {
	session      domain.CharacterSession
	nativeHandle int64
	dirty        bool
	ip           string
	expires      time.Time
	claiming     bool
	// uncertain marks a Finish whose commit outcome was lost. A resend or
	// expiry must first ask the store whether it already committed.
	uncertain bool
}

func (s *Server) prepareChannel(ctx context.Context, live *liveCharacter, target int, ip string) error {
	if target < 1 || target > s.wcfg.ChannelCount || !net.ParseIP(ip).IsLoopback() {
		return domain.ErrInvalidInput
	}
	// The aggregate still travels in memory, but a crash or timeout before the
	// destination claims it would otherwise drop unsaved progress.
	live.mu.Lock()
	dirty := live.dirty
	live.mu.Unlock()
	if dirty {
		if err := s.checkpoint(ctx, live); err != nil {
			return err
		}
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	session := live.session
	session.Lease = s.currentLease()
	dirty = live.dirty
	handle := live.nativeHandle
	live.mu.Unlock()
	if handle <= 0 {
		return domain.ErrTransferInvalid
	}
	if err := s.repo.BeginChannelTransfer(ctx, session, target); err != nil {
		return err
	}
	pending := &channelTransfer{session: session, nativeHandle: handle, dirty: dirty, ip: ip, expires: time.Now().Add(30 * time.Second)}
	s.mu.Lock()
	if s.channelTransfers == nil {
		s.channelTransfers = make(map[channelTransferKey]*channelTransfer)
	}
	s.channelTransfers[channelTransferKey{handle, target}] = pending
	s.mu.Unlock()
	slog.Info("channel_handoff_prepared", "character", session.CharacterID, "source", session.ChannelID, "target", target, "native_handle_matches_character", handle == session.CharacterID)
	return nil
}

func (s *Server) consumeChannel(ctx context.Context, handle int64, channel int, ip string) (*liveCharacter, bool, error) {
	key := channelTransferKey{handle, channel}
	s.mu.Lock()
	pending, exists := s.channelTransfers[key]
	if !exists {
		for _, candidate := range s.channelTransfers {
			if candidate.ip == ip && candidate.session.CharacterID == handle {
				slog.Warn("channel_handoff_native_character_id", "character", handle, "requested_channel", channel)
				break
			}
		}
		s.mu.Unlock()
		return nil, false, nil
	}
	if pending.claiming || pending.ip != ip || !time.Now().Before(pending.expires) {
		s.mu.Unlock()
		return nil, true, domain.ErrTransferInvalid
	}
	pending.claiming = true
	s.mu.Unlock()
	session := pending.session
	session.Lease = s.currentLease()
	next, err := s.repo.FinishChannelTransfer(ctx, session, channel)
	if err != nil && pending.uncertain {
		if resumed, resumeErr := s.resumeChannel(ctx, session, channel); resumeErr == nil {
			next, err = resumed, nil
			slog.Info("channel_handoff_resumed", "character", session.CharacterID, "target", channel, "generation", next.Generation)
		}
	}
	s.mu.Lock()
	if err == nil {
		delete(s.channelTransfers, key)
	} else {
		pending.claiming = false
		if !errors.Is(err, domain.ErrTransferInvalid) && !errors.Is(err, domain.ErrStaleOwner) && !errors.Is(err, domain.ErrInvalidInput) {
			pending.uncertain = true
		}
	}
	s.mu.Unlock()
	if err != nil {
		return nil, true, err
	}
	return &liveCharacter{session: next, dirty: pending.dirty, nativeHandle: pending.nativeHandle}, true, nil
}

// The native client changes sockets after processing the redirect. Closing the
// source immediately can race its stage transition and send it back to login.
// Retain only the transport until client EOF; never execute old-session input.
func awaitRedirectDisconnect(c *protocol.Conn) {
	c.SetTimeouts(30*time.Second, 5*time.Second)
	for range 64 {
		if _, err := c.ReadPacket(); err != nil {
			return
		}
	}
}

func (s *Server) resumeChannel(ctx context.Context, session domain.CharacterSession, channel int) (domain.CharacterSession, error) {
	repo, ok := s.repo.(domain.HandoffRecoveryRepository)
	if !ok {
		return domain.CharacterSession{}, domain.ErrTransferInvalid
	}
	return repo.ResumeChannelTransfer(ctx, session, channel)
}

func (s *Server) expireChannelTransfers(ctx context.Context) {
	type expiredTransfer struct {
		target  int
		pending channelTransfer
	}
	var expired []expiredTransfer
	s.mu.Lock()
	for key, pending := range s.channelTransfers {
		if !pending.claiming && !time.Now().Before(pending.expires) {
			expired = append(expired, expiredTransfer{key.channel, *pending})
			delete(s.channelTransfers, key)
		}
	}
	s.mu.Unlock()
	for _, item := range expired {
		session := item.pending.session
		session.Lease = s.currentLease()
		if item.pending.uncertain {
			// The destination committed but its client never got the field:
			// release the generation it produced, not the frozen source one.
			if next, err := s.resumeChannel(ctx, session, item.target); err == nil {
				if item.pending.dirty {
					if err = s.repo.SaveCharacter(ctx, next, next.Character); err != nil {
						slog.Warn("channel_handoff_expiry_save_failed", "character", next.CharacterID, "error", err)
					}
				}
				session = next
			}
		}
		// A reconnect using a new generation cannot be released by this cleanup.
		if err := s.repo.ReleaseCharacter(ctx, session); err != nil && !errors.Is(err, domain.ErrStaleOwner) {
			slog.Warn("channel_handoff_expiry_release_failed", "character", session.CharacterID, "error", err)
			continue
		}
		slog.Info("channel_handoff_expired", "character", session.CharacterID, "target", item.target, "generation", session.Generation)
	}
}
