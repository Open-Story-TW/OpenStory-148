package world

import (
	"context"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"os"
	"time"
)

type regenerationCounter struct {
	at   time.Time
	tick uint32
	seen bool
}

func (c regenerationCounter) eligible(tick uint32, now, entry time.Time, period time.Duration) bool {
	at := c.at
	if at.IsZero() {
		at = entry
	}
	return (!c.seen || int32(tick-c.tick) > 0) && !now.Before(at) && now.Sub(at) >= period
}

// persistMu -> CID publication gate -> short skill/live registry sections.
// Content is prepared outside the CID gate. No SQL, timer or replay worker is
// introduced: this acknowledges one native event against current live assets.
func (s *Server) regenerateAt(ctx context.Context, live *liveCharacter, sink packetSink, q protocol.Regeneration, now time.Time) error {
	_ = ctx
	if s.skills == nil || live == nil {
		return domain.ErrStaleOwner
	}
	visit := s.sceneFor(live, sink)
	if visit == nil {
		return domain.ErrStaleOwner
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	realClock := now.IsZero()
	if realClock {
		now = time.Now()
	}
	session := s.skillSession(live)
	before := session.Character
	if err := assetOutcomeUncertain(live, session); err != nil {
		return err
	}
	reply := func() error {
		release, err := s.lockScenePublication(session.CharacterID)
		if err != nil {
			return err
		}
		defer release()
		if !s.sceneCurrent(visit) {
			return domain.ErrStaleOwner
		}
		return sink.WritePacket(protocol.UpdateHPMP(before.HP, before.MP))
	}
	if s.combatVitalsPending(live, session) {
		return reply()
	}
	if before.HP <= 0 || q.HP < 0 || q.MP < 0 || q.HP == 0 && q.MP == 0 || q.Opcode != protocol.RecvRegeneration && q.Opcode != protocol.RecvPotentialRegeneration {
		return reply()
	}
	potential := q.Opcode == protocol.RecvPotentialRegeneration
	policy, policyErr := content.RequestedRecoveryPolicy(os.Getenv("MAPLEGO_WZ_ROOT"), before, now, potential, q.Mode)
	// A reviewed passive4s source owns its base+bonus component. Ordinary91
	// must not also add that same base; potential92 remains independent.
	if policyErr == nil && !potential {
		plans, e := skills.ReviewedRecoveryPlans(before, now)
		if e != nil {
			policyErr = e
		} else {
			for _, plan := range plans {
				if plan.HP > 0 {
					policy.HP = 0
				}
				if plan.MP > 0 {
					policy.MP = 0
				}
			}
		}
	}
	next := before
	if policyErr == nil && next.VitalsModel == 1 {
		next.MaxHP, next.MaxMP, policyErr = content.DerivedVitals(next, now)
	}
	if policyErr != nil || next.MaxHP < 1 || next.MaxMP < 0 || potential && next.VitalsModel != 1 {
		return reply()
	}
	next.HP = min(next.HP, next.MaxHP)
	next.MP = min(next.MP, next.MaxMP)
	unlock, err := s.lockScenePublication(session.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(visit) {
		return domain.ErrStaleOwner
	}
	// A timer may expire during first content loading or while waiting for the
	// scene gate. Reject that event without burning budget; the next request
	// derives from the new state. Never apply a pre-expiry cap after that wait.
	if realClock && recoveryExpiryCrossed(before, now, time.Now()) {
		return sink.WritePacket(protocol.UpdateHPMP(before.HP, before.MP))
	}
	member := s.skillMemberFor(session, live, sink)
	if member == nil {
		return domain.ErrStaleOwner
	}
	source := 0
	if potential {
		source = 1
	}
	claimed := [2]int{q.HP, q.MP}
	caps := [2]int{policy.HP, policy.MP}
	room := [2]int{next.MaxHP - next.HP, next.MaxMP - next.MP}
	gain := [2]int{}
	acceptedCaps := [2]int{}
	s.skills.mu.Lock()
	if s.skills.members[session.CharacterID] != member {
		s.skills.mu.Unlock()
		return domain.ErrStaleOwner
	}
	if !member.recoveryPose.allowsHP(visit, before) {
		caps[0] = 0
	}
	for component := 0; component < 2; component++ {
		counter := &member.regeneration[source][component]
		if claimed[component] > 0 && caps[component] > 0 && room[component] > 0 && counter.eligible(q.Tick, now, member.lastRegen, policy.Interval) {
			acceptedCaps[component] = caps[component]
			gain[component] = min(claimed[component], caps[component], room[component])
			*counter = regenerationCounter{at: now, tick: q.Tick, seen: true}
		}
	}
	s.skills.mu.Unlock()
	next.HP += gain[0]
	next.MP += gain[1]
	live.mu.Lock()
	// Every ordinary live vital writer owns persistMu; the scene gate also
	// excludes a replacement owner/visit until state and self packet publish.
	live.session.Character.HP, live.session.Character.MP = next.HP, next.MP
	live.session.Character.MaxHP, live.session.Character.MaxMP = next.MaxHP, next.MaxMP
	changed := next.HP != before.HP || next.MP != before.MP || next.MaxHP != before.MaxHP || next.MaxMP != before.MaxMP
	live.dirty = live.dirty || changed
	afterSession := live.session
	live.mu.Unlock()
	s.updateOnlineOwned(session, afterSession)
	if changed {
		s.recordRegenerationEvidence(session, q, before, next, acceptedCaps[0], acceptedCaps[1])
		s.refreshParty(live)
	}
	return sink.WritePacket(protocol.UpdateHPMP(next.HP, next.MP))
}

// Even malformed requests must not publish an old visit's vitals into a new
// field when a replacement/warp wins while the command waits for persistMu.
func (s *Server) resyncRegeneration(live *liveCharacter, sink packetSink) error {
	visit := s.sceneFor(live, sink)
	if visit == nil {
		return domain.ErrStaleOwner
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	if err := assetOutcomeUncertain(live, s.skillSession(live)); err != nil {
		return err
	}
	release, err := s.lockScenePublication(visit.owner.CharacterID)
	if err != nil {
		return err
	}
	defer release()
	if !s.sceneCurrent(visit) {
		return domain.ErrStaleOwner
	}
	live.mu.Lock()
	hp, mp := live.session.Character.HP, live.session.Character.MP
	live.mu.Unlock()
	return sink.WritePacket(protocol.UpdateHPMP(hp, mp))
}

func recoveryExpiryCrossed(c domain.Character, from, to time.Time) bool {
	for _, e := range c.Equipment {
		if e.ExpiresAt > 0 {
			at := time.UnixMilli(e.ExpiresAt)
			if at.After(from) && !at.After(to) {
				return true
			}
		}
	}
	for _, state := range c.Skills {
		if !state.ExpiresAt.IsZero() && state.ExpiresAt.After(from) && !state.ExpiresAt.After(to) {
			return true
		}
	}
	for _, buff := range c.SkillBuffs {
		if !buff.ExpiresAt.IsZero() && buff.ExpiresAt.After(from) && !buff.ExpiresAt.After(to) {
			return true
		}
	}
	return false
}
