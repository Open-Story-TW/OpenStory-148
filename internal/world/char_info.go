package world

import (
	"context"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// handleCharInfo answers 角色情報 for the requester or a character standing
// in the same field. The client itself only asks about characters it can see
// (0x9CFDA9 looks them up in its user pool) and drops a reply for anyone else.
func (s *Server) handleCharInfo(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	target, err := protocol.ReadCharInfoRequest(r)
	if err != nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	visit := s.sceneFor(live, sink)
	if visit == nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	other := live
	if target != visit.owner.CharacterID {
		s.mu.RLock()
		peer := s.scenes[target]
		s.mu.RUnlock()
		if peer == nil || peer.live == nil || peer.owner.ChannelID != visit.owner.ChannelID || peer.owner.Character.MapID != visit.owner.Character.MapID {
			return sink.WritePacket(protocol.EnableActions())
		}
		other = peer.live
	}
	other.mu.Lock()
	c := other.session.Character
	other.mu.Unlock()
	if c.ID != target {
		return sink.WritePacket(protocol.EnableActions())
	}
	packet := protocol.CharInfo(c, s.charInfoAlliance(ctx, other, c), other == live)
	if packet == nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	return sink.WritePacket(packet)
}

// charInfoAlliance is the alliance name of c's guild, or "" when it has none
// or the read fails (the window then just leaves the alliance line empty).
func (s *Server) charInfoAlliance(ctx context.Context, live *liveCharacter, c domain.Character) string {
	repo, ok := s.repo.(domain.AllianceRepository)
	if !ok || c.Guild.ID == 0 {
		return ""
	}
	call, done := context.WithTimeout(ctx, 2*time.Second)
	defer done()
	a, err := repo.ReadAlliance(call, s.petSession(live))
	if err != nil {
		return ""
	}
	return a.Name
}
