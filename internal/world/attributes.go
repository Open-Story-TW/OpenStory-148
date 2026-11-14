package world

import (
	"context"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) handleAttributeAllocation(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	request, err := protocol.ReadAttributeAllocation(r)
	if err != nil {
		return err
	}
	return s.allocateAttributes(ctx, live, sink, request)
}

func (s *Server) handleAttributeAutoAllocation(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	request, err := protocol.ReadAttributeAutoAllocation(r)
	if err != nil {
		return err
	}
	return s.allocateAttributes(ctx, live, sink, request)
}

func (s *Server) allocateAttributes(ctx context.Context, live *liveCharacter, sink packetSink, request domain.AttributeRequest) error {
	repo, ok := s.repo.(domain.AttributeRepository)
	if !ok || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	request.Session = live.session
	request.Session.Lease = s.currentLease()
	live.mu.Unlock()
	if s.combatVitalsPending(live, request.Session) {
		return sink.WritePacket(protocol.EnableActions())
	}
	result, err := repo.AllocateAttribute(ctx, request)
	if err != nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	s.applyProgressResult(live, result.Character)
	return sink.WritePacket(protocol.AttributeStats(result.Character))
}
