package world

import (
	"context"

	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func (s *Server) handleSkills(ctx context.Context, live *liveCharacter, sink packetSink, op uint16, r *protocol.Reader) (bool, error) {
	reject := func() (bool, error) { return true, sink.WritePacket(protocol.EnableActions()) }
	rejectVitals := func() (bool, error) {
		live.mu.Lock()
		hp, mp := live.session.Character.HP, live.session.Character.MP
		live.mu.Unlock()
		return true, sink.WritePacket(protocol.UpdateHPMP(hp, mp))
	}
	switch op {
	case protocol.RecvForceAtomLanded:
		// The client reports each thrown Phantom card that reached its
		// monster (0x54C65F); the card attack that follows is judged itself.
		r.Skip(r.Remaining())
		return true, nil
	case protocol.RecvSkillAllocate:
		q, err := protocol.ReadSkillAllocation(r)
		if err != nil || s.tradeActive(live) {
			return reject()
		}
		return true, s.allocateSkill(ctx, live, sink, q)
	case protocol.RecvSkillCast:
		q, err := protocol.ReadSkillCast(r)
		if err != nil || s.tradeActive(live) {
			return reject()
		}
		return true, s.castSkill(ctx, live, sink, q)
	case protocol.RecvSkillCancel:
		id, err := protocol.ReadSkillCancel(r)
		if err != nil {
			return reject()
		}
		s.endSkillPrepare(liveChannel(live), live, id)
		if skills.IsBlueAuraSkill(id) {
			return true, s.cancelBlueAura(ctx, live, sink)
		}
		return true, s.cancelSkill(ctx, live, sink, id)
	case protocol.RecvItemCancel:
		id, err := protocol.ReadItemCancel(r)
		if err != nil {
			return reject()
		}
		return true, s.cancelSkill(ctx, live, sink, id)
	case protocol.RecvTakeDamage:
		q, err := protocol.ReadPlayerDamage(r)
		if err != nil {
			return rejectVitals()
		}
		return true, s.takeDamage(ctx, live, sink, q)
	case protocol.RecvRegeneration, protocol.RecvPotentialRegeneration:
		q, err := protocol.ReadRegenerationOpcode(op, r)
		if err != nil {
			return true, s.resyncRegeneration(live, sink)
		}
		return true, s.regenerate(ctx, live, sink, q)
	}
	return false, nil
}
