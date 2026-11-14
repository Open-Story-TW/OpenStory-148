package world

import (
	"context"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) physicalSkillGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, a protocol.MeleeAttack) error {
	return s.settledMeleeGameplay(ctx, channel, live, sink, a, domain.CombatPhysicalSkill)
}
