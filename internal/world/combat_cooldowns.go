package world

import (
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Called only while merging a fresh, exact-owner result under persistMu and
// live.mu. Other cooldowns cannot be cleared by a partial/replayed result.
func mergeCommittedCombatCooldown(t *combatTicket, current *domain.Character, result domain.Character, now time.Time) {
	if t.generic == nil || t.request.Cost == nil || t.request.Cost.CooldownMS <= 0 || current.ID != t.actorSession.CharacterID {
		return
	}
	var found domain.SkillCooldown
	for _, cd := range result.SkillCooldowns {
		if cd.ID == t.request.SkillID && cd.ExpiresAt.After(now) && cd.ExpiresAt.After(found.ExpiresAt) {
			found = cd
		}
	}
	if found.ID == 0 {
		return
	}
	for _, cd := range current.SkillCooldowns {
		if cd.ID == found.ID && !cd.ExpiresAt.Before(found.ExpiresAt) {
			return
		}
	}
	next := make([]domain.SkillCooldown, 0, len(current.SkillCooldowns)+1)
	for _, cd := range current.SkillCooldowns {
		if cd.ID != found.ID {
			next = append(next, cd)
		}
	}
	current.SkillCooldowns = append(next, found)
	copy := found
	t.committedCooldown = &copy
}

// The caller holds this recipient's persistMu and has checked its exact room
// entry. This follows the existing persist -> skill publication lock order.
func (s *Server) publishCommittedCombatCooldown(t *combatTicket, p combatLivePeer, snapshot domain.CharacterSession) {
	cd := t.committedCooldown
	if cd == nil || t.cooldownPublished || p.id != t.actorSession.CharacterID || !cd.ExpiresAt.After(time.Now()) {
		return
	}
	if s.skills != nil {
		unlock := s.lockSkillPublication()
		defer unlock()
		m := s.skillMemberFor(snapshot, p.live, p.sink)
		if m == nil {
			return
		}
		if m.cooldowns == nil {
			m.cooldowns = map[int]time.Time{}
		}
		m.cooldowns[cd.ID] = cd.ExpiresAt
	}
	// Mark the attempted publication before I/O: an unknown write must never
	// trigger a second cooldown packet on receipt replay.
	t.cooldownPublished = true
	_ = p.sink.WritePacket(protocol.SkillCooldown(cd.ID, max(1, int(time.Until(cd.ExpiresAt).Seconds()+0.999))))
}
