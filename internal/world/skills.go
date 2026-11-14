package world

import (
	"context"
	"fmt"
	"log/slog"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
	"math/rand/v2"
	"sync"
	"time"
)

type skillMember struct {
	live                  *liveCharacter
	sink                  packetSink
	channel               int
	characterID           int64
	sessionID             string
	generation            int64
	worldID, mapID        int
	mapEntry              *mapMember
	lastDamage, lastRegen time.Time
	recoveryPose          recoveryPose
	regeneration          [2][2]regenerationCounter // source × HP/MP; guarded by skills.mu.
	cooldowns             map[int]time.Time         // Native-announced deadlines, guarded by publication.
	passiveTicks          map[int]time.Time         // Last OnTick per skill, guarded by skills.mu.
	damageTick            uint32
	// cards is the Phantom deck and cardKey the last thrown card, both
	// guarded by skills.mu; the deck is not kept across logins (Legacy).
	cards, cardKey int
	damageSeen     bool
}
type skillSpace struct {
	publication  sync.Mutex // Standalone test/runtime fallback without mapSpace.
	mu           sync.Mutex
	members      map[int64]*skillMember
	partyPending map[string]*partySkillTicket // guarded by mu; immutable request/peers
}

func newSkillSpace() *skillSpace { return &skillSpace{members: map[int64]*skillMember{}} }
func (s *Server) enterSkills(ctx context.Context, channel int, live *liveCharacter, sink packetSink) error {
	if s.skills == nil {
		return nil
	}
	unlock := s.lockSkillPublication()
	defer unlock()
	live.mu.Lock()
	session := cloneCombatSession(live.session)
	live.mu.Unlock()
	c := session.Character
	if session.ChannelID != channel {
		return domain.ErrStaleOwner
	}
	var entry *mapMember
	if s.maps != nil {
		s.maps.mu.Lock()
		entry = s.maps.rooms[mapKey{channel, c.MapID}][c.ID]
		s.maps.mu.Unlock()
		if entry == nil || !sameDropSink(entry.sink, sink) {
			return domain.ErrStaleOwner
		}
	}
	s.skills.mu.Lock()
	m := &skillMember{lastRegen: time.Now()}
	if previous := s.skills.members[c.ID]; previous != nil && previous.live == live && previous.sessionID == session.SessionID && previous.generation == session.Generation {
		*m = *previous
	}
	m.live, m.sink, m.channel = live, sink, channel
	m.characterID, m.sessionID, m.generation, m.worldID, m.mapID, m.mapEntry = session.CharacterID, session.SessionID, session.Generation, session.Lease.WorldID, c.MapID, entry
	if m.cooldowns == nil {
		m.cooldowns = make(map[int]time.Time)
	}
	for _, cooldown := range c.SkillCooldowns {
		if cooldown.ExpiresAt.After(m.cooldowns[cooldown.ID]) {
			m.cooldowns[cooldown.ID] = cooldown.ExpiresAt
		}
	}
	s.skills.members[c.ID] = m
	others := make([]*skillMember, 0, len(s.skills.members))
	for id, peer := range s.skills.members {
		if id != c.ID && peer.channel == channel && peer.mapID == c.MapID {
			others = append(others, peer)
		}
	}
	s.skills.mu.Unlock()
	now := time.Now()
	if err := s.syncNativeSkillCooldownsLocked(m, c, now); err != nil {
		return err
	}
	for _, group := range protocol.GroupSkillBuffs(activeSkillBuffs(c, now)) {
		if e := sink.WritePacket(protocol.SkillBuffsAmong(group, c.SkillBuffs, now)); e != nil {
			return e
		}
	}
	for _, peer := range others {
		s.publishCurrentSkillStateLocked(peer, m)
		s.publishCurrentSkillStateLocked(m, peer)
	}
	s.dropOnEnter(c, now)
	return nil
}

// dropOnEnter removes the rows whose handler's DropOnEnter rejects the
// entering holder (committed and published like an external buff change).
func (s *Server) dropOnEnter(c domain.Character, now time.Time) {
	done := map[int]bool{}
	for _, b := range activeSkillBuffs(c, now) {
		h, ok := skills.Lookup(b.ID)
		if done[b.ID] || !ok || h.DropOnEnter == nil || !h.DropOnEnter(c) {
			continue
		}
		done[b.ID] = true
		s.applyExternalBuff(c.ID, domain.SkillBuffChange{SkillID: b.ID, Level: b.Level, Cancel: true})
	}
}
func (s *Server) leaveSkills(characterID int64, expected ...*liveCharacter) {
	if s.skills == nil {
		return
	}
	unlock := s.lockSkillPublication()
	defer unlock()
	s.skills.mu.Lock()
	if len(expected) > 0 {
		member := s.skills.members[characterID]
		if member == nil || member.live != expected[0] {
			s.skills.mu.Unlock()
			return
		}
	}
	delete(s.skills.members, characterID)
	s.skills.mu.Unlock()
}
func (s *Server) mergeSkillResult(live *liveCharacter, r domain.SkillResult) domain.CharacterSession {
	live.mu.Lock()
	c := &live.session.Character
	c.Skills, c.SkillCooldowns, c.SkillBuffs = r.Character.Skills, r.Character.SkillCooldowns, r.Character.SkillBuffs
	if r.Changed {
		c.HP, c.MP, c.SP, c.Revision = r.Character.HP, r.Character.MP, r.Character.SP, r.Character.Revision
		c.MaxHP, c.MaxMP = r.Character.MaxHP, r.Character.MaxMP
		c.BaseMaxHP, c.BaseMaxMP, c.VitalsModel, c.VitalsOrigin = r.Character.BaseMaxHP, r.Character.BaseMaxMP, r.Character.VitalsModel, r.Character.VitalsOrigin
	}
	snapshot := cloneCombatSession(live.session)
	live.mu.Unlock()
	s.mu.Lock()
	if prior, ok := s.online[snapshot.CharacterID]; ok && prior.SessionID == snapshot.SessionID && prior.Generation == snapshot.Generation && prior.Lease.WorldID == snapshot.Lease.WorldID && prior.Lease.InstanceID == snapshot.Lease.InstanceID && prior.Lease.Epoch == snapshot.Lease.Epoch {
		s.online[snapshot.CharacterID] = snapshot
	}
	s.mu.Unlock()
	s.refreshParty(live)
	return snapshot
}
func (s *Server) skillSession(live *liveCharacter) domain.CharacterSession {
	live.mu.Lock()
	defer live.mu.Unlock()
	v := live.session
	v.Lease = s.currentLease()
	return v
}
func (s *Server) allocateSkill(ctx context.Context, live *liveCharacter, sink packetSink, q protocol.SkillAllocation) error {
	repo, ok := s.repo.(domain.SkillRepository)
	if !ok {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.skillSession(live)
	if s.skillMemberFor(session, live, sink) == nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	r, e := repo.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: session, SkillID: q.SkillID, Count: q.Count, OperationID: fmt.Sprintf("sp-%s-%d-%d", session.SessionID, session.Generation, q.Tick)})
	if e != nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	s.mergeSkillResult(live, r)
	for _, skill := range r.Character.Skills {
		if skill.ID == q.SkillID {
			if e = sink.WritePacket(protocol.SkillUpdate(skill)); e != nil {
				return e
			}
		}
	}
	return sink.WritePacket(protocol.SkillPointUpdate(r.Character.SP))
}
func (s *Server) castSkill(ctx context.Context, live *liveCharacter, sink packetSink, q protocol.SkillCast) error {
	if h, registered := skills.Lookup(q.SkillID); registered && h.Kind&skills.KindPartyBuff != 0 {
		return s.castPartySkill(ctx, live, sink, q)
	}
	repo, ok := s.repo.(domain.SkillRepository)
	if !ok {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.skillSession(live)
	source := s.skillMemberFor(session, live, sink)
	if source == nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	if phantomJudgment(q.SkillID) && !s.phantomDeckFull(source, session.Character.Job) {
		return sink.WritePacket(protocol.EnableActions())
	}
	castPeers := s.skillCastAudience(source)
	r, e := repo.CastSkill(ctx, domain.SkillCastRequest{Session: session, SkillID: q.SkillID, ClientLevel: q.Level, OperationID: fmt.Sprintf("cast-%s-%d-%d", session.SessionID, session.Generation, q.Tick)})
	if e != nil {
		slog.Info("skill_cast_rejected", "character", session.CharacterID, "skill", q.SkillID, "level", q.Level, "hp", session.Character.HP, "mp", session.Character.MP, "error", e)
		return sink.WritePacket(protocol.EnableActions())
	}
	snapshot := s.mergeSkillResult(live, r)
	e = s.publishSkillCast(source, castPeers, snapshot, r, q)
	s.syncGMHide(live)
	if e != nil || r.Replayed || !r.Effect {
		return e
	}
	s.castCombatObjects(snapshot, live, sink, q)
	if phantomJudgment(q.SkillID) {
		s.phantomDraw(source, snapshot.Character, q.SkillID, time.Now())
	}
	if r.MoveTo != 0 {
		// The warp takes persistMu, which this cast still holds.
		go s.castMove(snapshot.ChannelID, live, sink, r.MoveTo)
	}
	return nil
}

// castMove warps the caster after a cast with WZ moveTo (999999999 means
// the current map's return map).
func (s *Server) castMove(channel int, live *liveCharacter, sink packetSink, mapID int) {
	if s.gameplay == nil {
		return
	}
	live.mu.Lock()
	from := live.session.Character.MapID
	live.mu.Unlock()
	if mapID == 999999999 {
		field, err := s.gameplay.catalog.Map(from)
		if err != nil {
			return
		}
		mapID = field.ReturnMap
	}
	to, err := s.gameplay.catalog.Map(mapID)
	if err != nil || mapID == from {
		return
	}
	entry, ok := to.Spawn()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	if err = s.warpGameplay(ctx, channel, live, sink, to.ID, entry); err != nil {
		slog.Warn("skill_move_failed", "map", mapID, "error", err)
	}
}

// castCombatObjects runs the S0b side of a committed cast: summons, field
// objects (mist, smoke, doors) and monster casts. Failures leave the durable
// cast in place; the object is simply not created.
func (s *Server) castCombatObjects(snapshot domain.CharacterSession, live *liveCharacter, sink packetSink, q protocol.SkillCast) {
	spec, ok := attackSpecFor(q.SkillID)
	if !ok {
		return
	}
	now := time.Now()
	if spec.Summon != nil && !spec.Summon.NoCastSpawn {
		if _, err := s.SpawnSummon(snapshot.ChannelID, live, q.SkillID, q.Level, now); err != nil {
			slog.Warn("skill_summon_failed", "character", snapshot.CharacterID, "skill", q.SkillID, "error", err)
		}
	}
	if spec.Field != nil {
		if _, err := s.SpawnFieldObject(snapshot.ChannelID, live, q.SkillID, q.Level, now); err != nil {
			slog.Warn("skill_field_object_failed", "character", snapshot.CharacterID, "skill", q.SkillID, "error", err)
		}
	}
	if spec.MobCast != nil {
		if _, err := s.CastOnMobs(snapshot.ChannelID, live, q.SkillID, q.Level, q.Mobs, now); err != nil {
			slog.Warn("skill_mob_cast_failed", "character", snapshot.CharacterID, "skill", q.SkillID, "error", err)
		}
	}
	if spec.TeleportKey {
		_ = sink.WritePacket(protocol.RandomTeleportKey(1 + rand.IntN(99)))
	}
}
func (s *Server) cancelSkill(ctx context.Context, live *liveCharacter, sink packetSink, id int) error {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	return s.cancelSkillLocked(ctx, live, sink, id)
}

// Caller holds persistMu, including native summon removal which pins its
// owned object before cancelling the associated durable buff.
func (s *Server) cancelSkillLocked(ctx context.Context, live *liveCharacter, sink packetSink, id int) error {
	repo, ok := s.repo.(domain.SkillRepository)
	if !ok {
		return nil
	}
	session := s.skillSession(live)
	if s.skillMemberFor(session, live, sink) == nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	if err := assetOutcomeUncertain(live, session); err != nil {
		return err
	}
	if s.combatVitalsPending(live, session) {
		// Refuse a new cancellation while a receipt is unresolved; reassert
		// the current state without replaying a cast or affected effect.
		var rows []domain.SkillBuff
		for _, b := range activeSkillBuffs(session.Character, time.Now()) {
			if b.ID == id {
				rows = append(rows, b)
			}
		}
		if len(rows) > 0 {
			if e := sink.WritePacket(protocol.SkillBuffsAmong(rows, session.Character.SkillBuffs, time.Now())); e != nil {
				return e
			}
		}
		_ = sink.WritePacket(protocol.UpdateHPMP(session.Character.HP, session.Character.MP))
		return sink.WritePacket(protocol.EnableActions())
	}
	r, e := repo.CancelSkill(ctx, session, id)
	if e != nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	snap := s.mergeSkillResult(live, r)
	s.publishSkillTick(snap, sink, r, session.Character.HP)
	s.syncGMHide(live)
	if spec, ok := attackSpecFor(id); ok && spec.Summon != nil {
		s.RemoveSummons(snap.ChannelID, snap.Character.MapID, snap.CharacterID, id, snap)
	}
	return sink.WritePacket(protocol.EnableActions())
}
func (s *Server) runSkills(ctx context.Context) {
	repo, ok := s.repo.(domain.SkillRepository)
	if !ok || s.skills == nil {
		return
	}
	combat.ObserveAttacks(s.observeSkillAttack)
	skills.SetBuffApplier(s.applyExternalBuff)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if partyRepo, ok := s.repo.(domain.PartySkillRepository); ok {
				s.resolvePartySkills(ctx, partyRepo)
			}
			s.skills.mu.Lock()
			members := make([]*skillMember, 0, len(s.skills.members))
			for _, m := range s.skills.members {
				members = append(members, m)
			}
			s.skills.mu.Unlock()
			for _, m := range members {
				s.expireSkillCooldowns(m, now)
				s.tickSkillPassives(m, now)
				s.publishStatDetail(m.live, m.sink, now, false)
				m.live.mu.Lock()
				due := false
				for _, b := range m.live.session.Character.SkillBuffs {
					if !b.ExpiresAt.After(now) || m.live.session.Character.HP == 0 || (b.Stat == "RECOVERY" || b.Stat == "INFINITY") && !b.NextTick.After(now) {
						due = true
					}
				}
				m.live.mu.Unlock()
				if !due {
					continue
				}
				s.tickSkillMember(ctx, repo, m)
			}
		}
	}
}

// Keep due recovery/expiry work pending while a combat receipt is unknown.
// The next regular timer iteration retries from durable state after resolution;
// no rejected transient damage or regeneration request is retained/replayed.
func (s *Server) tickSkillMember(ctx context.Context, repo domain.SkillRepository, m *skillMember) {
	step, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m.live.persistMu.Lock()
	defer m.live.persistMu.Unlock()
	session := s.skillSession(m.live)
	if s.skillMemberFor(session, m.live, m.sink) != m || s.combatVitalsPending(m.live, session) {
		return
	}
	r, e := repo.TickSkillBuffs(step, session)
	if e == nil {
		snap := s.mergeSkillResult(m.live, r)
		s.publishSkillTick(snap, m.sink, r, session.Character.HP)
		s.syncGMHide(m.live)
	}
}

func (s *Server) takeDamage(ctx context.Context, live *liveCharacter, sink packetSink, q protocol.PlayerDamage) error {
	if s.skills == nil || s.gameplay == nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.skillSession(live)
	c := session.Character
	if err := assetOutcomeUncertain(live, session); err != nil {
		return err
	}
	if s.combatVitalsPending(live, session) {
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	expectedMember := s.skillMemberFor(session, live, sink)
	if expectedMember == nil {
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	if c.HP <= 0 {
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[mapKey{channel: session.ChannelID, mapID: c.MapID}]
	valid := false
	limit := 0
	if room != nil {
		mob := room.mobs[q.ObjectID]
		if mob != nil && mob.hp > 0 && mob.stats.ID == q.MonsterID && within(c.X, c.Y, mob.life.X, mob.life.Y, 220) {
			valid = true
			limit = max(1, mob.stats.PAD*2)
		}
	}
	s.gameplay.mu.Unlock()
	if !valid || q.Damage > limit {
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	s.skills.mu.Lock()
	member := s.skills.members[c.ID]
	now := time.Now()
	if member != expectedMember || member.damageSeen && (member.damageTick == q.Tick || now.Sub(member.lastDamage) < time.Second) {
		s.skills.mu.Unlock()
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	member.damageTick = q.Tick
	member.damageSeen = true
	member.lastDamage = now
	s.skills.mu.Unlock()
	// A party Smokescreen covering the victim cancels the hit.
	if s.PlayerInArea(session.ChannelID, c.MapID, c.X, c.Y, s.smokeOwners(c.ID)) {
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	live.mu.Lock()
	hpLoss, mpLoss := skills.TakenDamage(live.session.Character, q.Damage, now)
	if _, shielded := skills.ActiveStat(live.session.Character, "DIVINE_SHIELD", now); shielded {
		// Java: an active Divine Shield blocks the whole hit; the owning
		// skill's OnDamaged counts the block and breaks the shield.
		hpLoss, mpLoss = 0, 0
	}
	if live.god {
		// GM !god: the hit is reported and answered, never applied.
		hpLoss, mpLoss = 0, 0
	}
	lethal := live.session.Character.HP-hpLoss <= 0
	live.session.Character.HP = max(0, live.session.Character.HP-hpLoss)
	live.session.Character.MP = max(0, live.session.Character.MP-mpLoss)
	live.dirty = true
	c = live.session.Character
	live.mu.Unlock()
	if lethal {
		c = s.reviveOnLethal(live, expectedMember, q, c, now)
	}
	s.refreshParty(live)
	_ = sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	if c.HP > 0 {
		go s.runSkillTriggers(expectedMember, nil, q.Damage, q.ObjectID, q.DefType)
	}
	packet := protocol.PlayerDamagePacket(c.ID, q)
	s.publishSkillForeign(session, live, sink, packet)
	if c.HP == 0 {
		if err := s.clearDeathBuffs(ctx, live, sink); err != nil {
			// HP remains authoritatively dead/dirty. The timer retries, and a
			// return is blocked until cancellation can commit successfully.
			slog.Warn("death_buff_cancellation_failed", "character", c.ID, "error", err)
		}
	}
	return nil
}

// syncGMHide applies the live GM_HIDE buff state to map visibility. It
// must run without the skill/map publication lock held.
func (s *Server) syncGMHide(live *liveCharacter) {
	if s.maps == nil {
		return
	}
	live.mu.Lock()
	c := live.session.Character
	channel := live.session.ChannelID
	live.mu.Unlock()
	_, hidden := skills.ActiveStat(c, "GM_HIDE", time.Now())
	if c.HP <= 0 {
		hidden = false
	}
	s.maps.SetHidden(channel, c.MapID, c.ID, hidden)
}

// smokeOwners are the characters whose Smokescreen protects id: itself and
// its party members.
func (s *Server) smokeOwners(id int64) map[int64]bool {
	owners := map[int64]bool{id: true}
	if s.parties == nil {
		return owners
	}
	s.parties.mu.Lock()
	defer s.parties.mu.Unlock()
	if p := s.parties.peers[id]; p != nil {
		for _, m := range p.party.Members {
			owners[m.CharacterID] = true
		}
	}
	return owners
}

func (s *Server) regenerate(ctx context.Context, live *liveCharacter, sink packetSink, q protocol.Regeneration) error {
	return s.regenerateAt(ctx, live, sink, q, time.Time{})
}
