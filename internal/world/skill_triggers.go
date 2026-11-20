package world

import (
	"context"
	"slices"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// observeSkillAttack is the combat.ObserveAttacks callback: it must not block,
// so the buff work runs on its own goroutine after the attack is applied.
func (s *Server) observeSkillAttack(e combat.AttackEvent) {
	if s.skills == nil || e.Summon {
		return
	}
	s.skills.mu.Lock()
	m := s.skills.members[e.Session.CharacterID]
	s.skills.mu.Unlock()
	if m == nil || m.sessionID != e.Session.SessionID || m.generation != e.Session.Generation {
		return
	}
	go s.runSkillTriggers(m, &e, 0, 0, 0)
	go s.phantomCards(m, &e)
}

// runSkillTriggers commits the OnAttack/OnDamaged buff changes of m's
// learned skills and publishes the resulting buffs and affected animations.
func (s *Server) runSkillTriggers(m *skillMember, attack *combat.AttackEvent, damage int, mob int32, defType byte) {
	if _, ok := s.repo.(domain.SkillTriggerRepository); !ok {
		return
	}
	m.live.persistMu.Lock()
	session := s.skillSession(m.live)
	if s.skillMemberFor(session, m.live, m.sink) != m || s.combatVitalsPending(m.live, session) || session.Character.HP <= 0 {
		m.live.persistMu.Unlock()
		return
	}
	var mobs []skills.TriggerMob
	if attack == nil {
		mobs = s.triggerMobs(m.channel, session.Character.MapID)
	}
	changes, hits := skills.Triggers(session.Character, time.Now(), attack, damage, mob, mobs, defType)
	s.commitSkillChanges(m, session, changes, false)
	m.live.persistMu.Unlock()
	// DamageMobs and CastOnMobs take their own locks (DamageMobs persistMu).
	for _, h := range hits {
		if len(h.Hits) > 0 {
			step, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.DamageMobs(step, m.channel, m.live, m.sink, h.SkillID, h.Hits)
			cancel()
		}
		if len(h.Cast) > 0 {
			_, _ = s.CastOnMobs(m.channel, m.live, h.SkillID, h.Level, h.Cast, time.Now())
		}
	}
}

// triggerMobs lists the living monsters of a room for OnDamaged triggers.
func (s *Server) triggerMobs(channel, mapID int) []skills.TriggerMob {
	if s.gameplay == nil {
		return nil
	}
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	room := s.gameplay.rooms[mapKey{channel: channel, mapID: mapID}]
	if room == nil {
		return nil
	}
	out := make([]skills.TriggerMob, 0, len(room.mobs))
	for _, mob := range room.mobs {
		if mob.hp > 0 {
			out = append(out, skills.TriggerMob{ObjectID: mob.objectID, X: mob.life.X, Y: mob.life.Y, MaxHP: mob.stats.HP})
		}
	}
	return out
}

// reviveOnLethal runs OnLethal (caller holds persistMu): a reviving change
// commits with the dead vitals already applied, then HP is restored to the
// requested percent (Soul Stone, Final Feint). It returns the new character.
func (s *Server) reviveOnLethal(live *liveCharacter, m *skillMember, q protocol.PlayerDamage, c domain.Character, now time.Time) domain.Character {
	change, revive, ok := skills.Lethal(c, now, q.Damage, q.ObjectID)
	if !ok {
		return c
	}
	live.mu.Lock()
	live.session.Character.HP = max(1, live.session.Character.MaxHP*revive.ReviveHPPercent/100)
	live.dirty = true
	c = live.session.Character
	live.mu.Unlock()
	session := s.skillSession(live)
	if change.Cancel || len(change.Stats) > 0 || change.Cooldown > 0 || change.HealHP > 0 || change.HealMP > 0 || change.CostMP > 0 {
		s.commitSkillChanges(m, session, []domain.SkillBuffChange{change}, false)
	}
	if revive.ReviveAtSpawn {
		go s.reviveToSpawn(m.channel, live, m.sink)
	}
	live.mu.Lock()
	c = live.session.Character
	live.mu.Unlock()
	return c
}

// reviveToSpawn moves a revived character to the current map's spawn
// point 0.
func (s *Server) reviveToSpawn(channel int, live *liveCharacter, sink packetSink) {
	if s.gameplay == nil {
		return
	}
	live.mu.Lock()
	mapID := live.session.Character.MapID
	live.mu.Unlock()
	field, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return
	}
	entry, ok := field.Spawn()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.warpGameplay(ctx, channel, live, sink, field.ID, entry)
}

// tickSkillPassives runs due OnTick handlers of m's learned skills.
func (s *Server) tickSkillPassives(m *skillMember, now time.Time) {
	m.live.mu.Lock()
	c := m.live.session.Character
	m.live.mu.Unlock()
	if c.HP <= 0 {
		return
	}
	// The two reviewed recovery skills own a runtime-only source, with fresh
	// policy and budget checks after persistMu/pending/visit fencing. Other
	// trigger behavior remains unchanged and is not claimed outcome-safe here.
	filtered := make([]domain.SkillState, 0, len(c.Skills))
	hasRecovery := false
	for _, state := range c.Skills {
		if skills.IsReviewedRecoverySkill(state.ID) {
			hasRecovery = true
			continue
		}
		filtered = append(filtered, state)
	}
	if hasRecovery {
		go s.tickReviewedRecoveryAt(m, time.Time{})
	}
	c.Skills = filtered
	s.skills.mu.Lock()
	if m.passiveTicks == nil {
		m.passiveTicks = map[int]time.Time{}
	}
	changes := skills.Ticks(c, now, m.passiveTicks)
	s.skills.mu.Unlock()
	if len(changes) == 0 {
		return
	}
	go func() {
		m.live.persistMu.Lock()
		defer m.live.persistMu.Unlock()
		session := s.skillSession(m.live)
		if s.skillMemberFor(session, m.live, m.sink) != m || s.combatVitalsPending(m.live, session) || session.Character.HP <= 0 {
			return
		}
		s.commitSkillChanges(m, session, changes, false)
	}()
}

// applyExternalBuff is the skills.ApplyBuff path (summon sub-skills).
func (s *Server) applyExternalBuff(ownerID int64, change domain.SkillBuffChange) {
	if s.skills == nil {
		return
	}
	s.skills.mu.Lock()
	m := s.skills.members[ownerID]
	s.skills.mu.Unlock()
	if m == nil {
		return
	}
	go func() {
		m.live.persistMu.Lock()
		defer m.live.persistMu.Unlock()
		session := s.skillSession(m.live)
		if s.skillMemberFor(session, m.live, m.sink) != m || s.combatVitalsPending(m.live, session) || session.Character.HP <= 0 {
			return
		}
		s.commitSkillChanges(m, session, []domain.SkillBuffChange{change}, false)
	}()
}

// commitSkillChanges commits changes for m (caller holds persistMu) and
// publishes them.
func (s *Server) commitSkillChanges(m *skillMember, session domain.CharacterSession, changes []domain.SkillBuffChange, comboReply bool) {
	repo, ok := s.repo.(domain.SkillTriggerRepository)
	if !ok || len(changes) == 0 {
		return
	}
	step, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := repo.ChangeSkillBuffs(step, session, changes)
	if err != nil {
		return
	}
	before := session.Character.SkillBuffs
	snapshot := s.mergeSkillResult(m.live, r)
	s.publishSkillChanges(m, snapshot, r, changes, before, comboReply)
	s.syncGMHide(m.live)
}

// sameStatValues reports whether rows carry the same stat values as the
// previous rows of that skill (only the server-only Counter moved).
func sameStatValues(before []domain.SkillBuff, rows []domain.SkillBuff, now time.Time) bool {
	old := map[string]int{}
	for _, b := range before {
		if len(rows) > 0 && b.ID == rows[0].ID && b.ExpiresAt.After(now) {
			old[b.Stat] = b.Value
		}
	}
	if len(old) != len(rows) {
		return false
	}
	for _, b := range rows {
		if v, ok := old[b.Stat]; !ok || v != b.Value {
			return false
		}
	}
	return true
}

// publishSkillChanges sends the cancelled and replaced rows of trigger
// changes to the owner and visible peers.
func (s *Server) publishSkillChanges(m *skillMember, snapshot domain.CharacterSession, r domain.SkillResult, changes []domain.SkillBuffChange, before []domain.SkillBuff, comboReply bool) {
	unlock := s.lockSkillPublication()
	defer unlock()
	current, valid := s.skillCurrent(m)
	if !valid || current.SessionID != snapshot.SessionID || current.Generation != snapshot.Generation {
		return
	}
	now := time.Now()
	peers := s.skillPeers(m.characterID, m.channel, m.mapID)
	s.publishSkillExpiry(m, m.sink, peers, current.Character, r.Expired, now)
	for _, cool := range r.Cooldowns {
		if m.cooldowns == nil {
			m.cooldowns = make(map[int]time.Time)
		}
		m.cooldowns[cool.ID] = cool.ExpiresAt
		_ = m.sink.WritePacket(protocol.SkillCooldown(cool.ID, max(1, int(time.Until(cool.ExpiresAt).Seconds()+0.999))))
	}
	for _, ch := range changes {
		if ch.Cancel {
			if spec, ok := attackSpecFor(ch.SkillID); ok && spec.Summon != nil {
				s.RemoveSummons(m.channel, m.mapID, m.characterID, ch.SkillID)
			}
			continue
		}
		var rows []domain.SkillBuff
		for _, b := range activeSkillBuffs(current.Character, now) {
			if b.ID == ch.SkillID {
				rows = append(rows, b)
			}
		}
		if len(rows) == 0 {
			continue
		}
		h, _ := skills.Lookup(ch.SkillID)
		if h.CounterPacket != nil {
			if comboReply {
				_ = m.sink.WritePacket(h.CounterPacket(rows[0].Counter))
			}
			if sameStatValues(before, rows, now) {
				continue // Only the counter moved: no buff re-send.
			}
		}
		_ = m.sink.WritePacket(protocol.SkillBuffsAmong(rows, current.Character.SkillBuffs, now))
		showAffected := h.TriggerAffectedEffect && !slices.ContainsFunc(before, func(row domain.SkillBuff) bool {
			return row.ID == ch.SkillID && row.ExpiresAt.After(now)
		})
		if showAffected {
			_ = m.sink.WritePacket(protocol.SkillAffectedEffect(0, ch.SkillID, rows[0].Level))
		}
		for _, target := range peers {
			if s.skillVisible(m, target) {
				_ = target.sink.WritePacket(protocol.ForeignSkillBuffs(m.characterID, rows))
				if showAffected {
					_ = target.sink.WritePacket(protocol.SkillAffectedEffect(m.characterID, ch.SkillID, rows[0].Level))
				}
			}
		}
	}
	if touchesMaxima(r.Expired) || len(r.Expired) > 0 && touchesMaxima(current.Character.SkillBuffs) {
		_ = m.sink.WritePacket(protocol.InventoryVitals(current.Character))
	} else {
		for _, ch := range changes {
			if ch.HealHP > 0 || ch.HealMP > 0 || ch.CostMP > 0 {
				_ = m.sink.WritePacket(protocol.UpdateHPMP(current.Character.HP, current.Character.MP))
				break
			}
		}
	}
}

// clientCombo handles RecvAranCombo (0xEA, no body): Java PlayerHandler
// AranCombo for jobs 2000-2112. OnClientCombo changes are committed and a
// CounterPacket skill answers with its Counter (testCombo 0x153); the server
// never pushes 0x153 on its own (the client faults on an unrequested one).
func (s *Server) clientCombo(live *liveCharacter, sink packetSink) {
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	if c.Job < 2000 || c.Job > 2112 || c.HP <= 0 || s.skills == nil {
		return
	}
	s.skills.mu.Lock()
	m := s.skills.members[c.ID]
	s.skills.mu.Unlock()
	if m == nil || m.live != live || m.sessionID != session.SessionID || m.generation != session.Generation {
		return
	}
	if _, ok := s.repo.(domain.SkillTriggerRepository); !ok {
		return
	}
	m.live.persistMu.Lock()
	defer m.live.persistMu.Unlock()
	current := s.skillSession(m.live)
	if s.skillMemberFor(current, m.live, m.sink) != m || s.combatVitalsPending(m.live, current) || current.Character.HP <= 0 {
		return
	}
	s.commitSkillChanges(m, current, skills.ClientComboTriggers(current.Character, time.Now()), true)
}
