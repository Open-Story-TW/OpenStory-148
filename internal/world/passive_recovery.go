package world

import (
	"log/slog"
	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"time"
)

// Periodic passive recovery updates live vitals. The normal character
// checkpoints persist the result.
func (s *Server) tickReviewedRecoveryAt(m *skillMember, now time.Time) {
	visit := s.sceneFor(m.live, m.sink)
	if visit == nil {
		return
	}
	m.live.persistMu.Lock()
	defer m.live.persistMu.Unlock()
	realClock := now.IsZero()
	if realClock {
		now = time.Now()
	}
	session := s.skillSession(m.live)
	if s.skillMemberFor(session, m.live, m.sink) != m || s.combatVitalsPending(m.live, session) || session.Character.HP <= 0 {
		return
	}
	before := session.Character
	plans, err := skills.ReviewedRecoveryPlans(before, now)
	if err != nil || len(plans) == 0 {
		return
	}
	next := before
	if next.VitalsModel == 1 {
		next.MaxHP, next.MaxMP, err = content.DerivedVitals(next, now)
		if err != nil {
			return
		}
	}
	if next.MaxHP < 1 || next.MaxMP < 0 {
		return
	}
	next.HP = min(next.HP, next.MaxHP)
	next.MP = min(next.MP, next.MaxMP)
	release, err := s.lockScenePublication(session.CharacterID)
	if err != nil {
		return
	}
	defer release()
	if !s.sceneCurrent(visit) || s.skillMemberFor(session, m.live, m.sink) != m {
		return
	}
	if realClock && recoveryExpiryCrossed(before, now, time.Now()) {
		return
	}
	applied := false
	type recoveryRecord struct{ id, hp, mp, afterHP, afterMP int }
	var records []recoveryRecord
	s.skills.mu.Lock()
	if s.skills.members[session.CharacterID] != m {
		s.skills.mu.Unlock()
		return
	}
	if m.passiveTicks == nil {
		m.passiveTicks = map[int]time.Time{}
	}
	for _, plan := range plans {
		at, seen := m.passiveTicks[plan.SkillID]
		if !seen {
			m.passiveTicks[plan.SkillID] = now
			continue
		}
		if now.Before(at) || now.Sub(at) < plan.Interval {
			continue
		}
		m.passiveTicks[plan.SkillID] = now
		oldHP, oldMP := next.HP, next.MP
		next.HP = min(next.MaxHP, next.HP+plan.HP)
		next.MP = min(next.MaxMP, next.MP+plan.MP)
		applied = applied || next.HP != oldHP || next.MP != oldMP
		if next.HP != oldHP || next.MP != oldMP {
			records = append(records, recoveryRecord{plan.SkillID, oldHP, oldMP, next.HP, next.MP})
		}
	}
	s.skills.mu.Unlock()
	changed := applied || before.HP != next.HP || before.MP != next.MP || before.MaxHP != next.MaxHP || before.MaxMP != next.MaxMP
	if !changed {
		return
	}
	m.live.mu.Lock()
	m.live.session.Character.HP, m.live.session.Character.MP = next.HP, next.MP
	m.live.session.Character.MaxHP, m.live.session.Character.MaxMP = next.MaxHP, next.MaxMP
	m.live.dirty = true
	after := m.live.session
	m.live.mu.Unlock()
	if combatEvidenceEnabled(session.CharacterID) {
		for _, row := range records {
			slog.Info("passive_regeneration_applied", "character", session.CharacterID, "world", session.Lease.WorldID, "channel", session.ChannelID, "map", next.MapID, "skill", row.id, "hp_before", row.hp, "mp_before", row.mp, "hp_after", row.afterHP, "mp_after", row.afterMP, "source", "server_skill_tick", "durable_checkpoint_verified", false)
		}
	}
	s.updateOnlineOwned(session, after)
	s.refreshParty(m.live)
	_ = m.sink.WritePacket(protocol.UpdateHPMP(next.HP, next.MP))
}
