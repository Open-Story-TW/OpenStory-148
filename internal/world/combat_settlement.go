package world

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type combatLivePeer struct {
	id      int64
	live    *liveCharacter
	sink    packetSink
	entry   *gameplayMember
	session domain.CharacterSession
}
type combatTicket struct {
	request            domain.CombatAttackRequest
	hash               string
	key                mapKey
	actor              *liveCharacter
	sink               packetSink
	actorSession       domain.CharacterSession
	targets            []*gameplayMob
	peers              []combatLivePeer
	observers          []combatLivePeer
	attack             protocol.MeleeAttack
	magic              *protocol.MagicAttack
	magicLevel         int
	generic            *genericAttack
	resolving          bool
	damageEvidence     []appliedDamageEvidence
	expectedActorEntry *gameplayMember
	rewardNotified     map[int64]bool // guarded by recipient persistence locks
	committedCooldown  *domain.SkillCooldown
	cooldownPublished  bool // guarded by the actor persistMu
}

func cloneCombatSession(s domain.CharacterSession) domain.CharacterSession {
	c := &s.Character
	c.Quests = append([]domain.QuestState(nil), c.Quests...)
	for i := range c.Quests {
		c.Quests[i].MobOrder = append([]int(nil), c.Quests[i].MobOrder...)
		old := c.Quests[i].MobKills
		c.Quests[i].MobKills = map[int]int{}
		for id, n := range old {
			c.Quests[i].MobKills[id] = n
		}
	}
	c.Equipment = append([]domain.Equipment(nil), c.Equipment...)
	c.Items = append([]domain.InventoryItem(nil), c.Items...)
	c.Skills = append([]domain.SkillState(nil), c.Skills...)
	c.SkillBuffs = append([]domain.SkillBuff(nil), c.SkillBuffs...)
	c.SkillCooldowns = append([]domain.SkillCooldown(nil), c.SkillCooldowns...)
	return s
}

func addCombatDamage(damage []domain.CombatContribution, s domain.CharacterSession, amount int) []domain.CombatContribution {
	out := append([]domain.CombatContribution(nil), damage...)
	if amount <= 0 {
		return out
	}
	for i, d := range out {
		if d.CharacterID == s.CharacterID && d.SessionID == s.SessionID && d.Generation == s.Generation {
			out[i].Damage += int64(amount)
			return out
		}
	}
	return append(out, domain.CombatContribution{CharacterID: s.CharacterID, SessionID: s.SessionID, Generation: s.Generation, Damage: int64(amount)})
}

func ticketContributors(t *combatTicket) []int64 {
	ids := map[int64]bool{}
	for _, q := range t.request.Targets {
		for _, d := range q.Contributions {
			ids[d.CharacterID] = true
		}
	}
	out := make([]int64, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *Server) lockCombatPeers(t *combatTicket, parties []domain.CombatPartyPin) (func(), error) {
	needed := map[int64]bool{t.actorSession.CharacterID: true}
	for _, id := range ticketContributors(t) {
		needed[id] = true
	}
	for _, p := range parties {
		for _, id := range p.Members {
			needed[id] = true
		}
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[t.key]
	var peers []combatLivePeer
	if room != nil {
		seenLive := map[*liveCharacter]bool{}
		for id := range needed {
			if member := room.members[id]; member != nil && member.live != nil {
				if seenLive[member.live] {
					g.mu.Unlock()
					return nil, domain.ErrForbidden
				}
				seenLive[member.live] = true
				peers = append(peers, combatLivePeer{id: id, live: member.live, sink: member.sink, entry: member})
			}
		}
	}
	g.mu.Unlock()
	sort.Slice(peers, func(i, j int) bool { return peers[i].id < peers[j].id })
	for _, p := range peers {
		p.live.persistMu.Lock()
	}
	unlock := func() {
		for i := len(peers) - 1; i >= 0; i-- {
			peers[i].live.persistMu.Unlock()
		}
	}
	lease := s.currentLease()
	t.request.Members = nil
	t.peers = nil
	foundActor := false
	for _, p := range peers {
		p.live.mu.Lock()
		snapshot := cloneCombatSession(p.live.session)
		p.live.mu.Unlock()
		snapshot.Lease = lease
		g.mu.Lock()
		room = g.rooms[t.key]
		valid := room != nil && room.members[snapshot.CharacterID] == p.entry && p.entry.live == p.live && sameDropSink(p.entry.sink, p.sink)
		g.mu.Unlock()
		if !valid || snapshot.CharacterID != p.id || snapshot.SessionID != p.entry.sessionID || snapshot.Generation != p.entry.generation || snapshot.Lease.WorldID != p.entry.worldID || snapshot.ChannelID != t.key.channel || snapshot.Character.MapID != t.key.mapID {
			continue
		}
		if snapshot.CharacterID == t.actorSession.CharacterID {
			if p.live != t.actor || !sameDropSink(p.sink, t.sink) || snapshot.SessionID != t.actorSession.SessionID || snapshot.Generation != t.actorSession.Generation || t.expectedActorEntry != nil && p.entry != t.expectedActorEntry {
				unlock()
				return nil, domain.ErrStaleOwner
			}
			t.request.Session = snapshot
			foundActor = true
		}
		p.session = snapshot
		t.peers = append(t.peers, p)
		t.request.Members = append(t.request.Members, snapshot)
	}
	if !foundActor {
		unlock()
		return nil, domain.ErrStaleOwner
	}
	t.request.Parties = parties
	return unlock, nil
}

func (s *Server) releaseCombatClaims(t *combatTicket) {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, mob := range t.targets {
		if mob.reservation == t {
			mob.reservation = nil
			mob.claiming = false
		}
	}
	releaseEruptionClaimsLocked(t)
	delete(g.pendingCombat, t.request.OperationID)
}

func combatKnownFailure(err error) bool {
	return errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrStaleOwner)
}

// Known rollback restores native optimistic HP/MP only for the still-current
// actor scope. Unknown commits intentionally wait for durable receipt lookup.
func (s *Server) rejectCombatTicket(t *combatTicket) error {
	t.actor.persistMu.Lock()
	defer t.actor.persistMu.Unlock()
	t.actor.mu.Lock()
	current := t.actor.session
	t.actor.mu.Unlock()
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[t.key]
	valid := room != nil && room.members[current.CharacterID] != nil && room.members[current.CharacterID].live == t.actor && sameDropSink(room.members[current.CharacterID].sink, t.sink)
	s.gameplay.mu.Unlock()
	if !valid || current.SessionID != t.actorSession.SessionID || current.Generation != t.actorSession.Generation || current.ChannelID != t.key.channel || current.Character.MapID != t.key.mapID {
		return nil
	}
	if err := t.sink.WritePacket(protocol.UpdateHPMP(current.Character.HP, current.Character.MP)); err != nil {
		return err
	}
	return t.sink.WritePacket(protocol.EnableActions())
}

func (s *Server) executeCombatTicket(ctx context.Context, repo domain.CombatSettlementRepository, t *combatTicket) error {
	for attempt := 0; attempt < 2; attempt++ {
		step, cancel := context.WithTimeout(ctx, 3*time.Second)
		parties, err := repo.CombatParties(step, s.currentLease(), ticketContributors(t))
		cancel()
		if err != nil {
			s.releaseCombatClaims(t)
			return s.rejectCombatTicket(t)
		}
		unlock, err := s.lockCombatPeers(t, parties)
		if err != nil {
			s.releaseCombatClaims(t)
			return s.rejectCombatTicket(t)
		}
		s.gameplay.mu.Lock()
		areasCurrent := s.eruptionClaimsCurrentLocked(t, time.Now()) &&
			s.expeditionCombatAllowedLocked(t.key, t.actorSession.CharacterID)
		s.gameplay.mu.Unlock()
		if !areasCurrent {
			unlock()
			s.releaseCombatClaims(t)
			return s.rejectCombatTicket(t)
		}
		t.hash, err = domain.CombatManifestHash(t.request)
		if err != nil {
			unlock()
			s.releaseCombatClaims(t)
			return err
		}
		step, cancel = context.WithTimeout(ctx, 3*time.Second)
		result, err := repo.ApplyCombatAttack(step, t.request)
		cancel()
		if err == nil {
			s.commitCombatTicket(t, result)
			s.publishCombatProgressLocked(t, result)
			unlock()
			s.publishCombatTicket(ctx, t, result)
			return nil
		}
		if errors.Is(err, domain.ErrStaleOwner) && attempt == 0 {
			unlock()
			continue
		}
		if combatKnownFailure(err) {
			unlock()
			slog.Warn("combat_settlement_rejected", "character", t.request.Session.CharacterID, "skill", t.request.SkillID, "family", t.request.Family, "error", err)
			s.releaseCombatClaims(t)
			return s.rejectCombatTicket(t)
		}
		// Outcome takes the SAME transaction advisory lock as Apply. A nil result
		// therefore proves the original transaction has ended without committing.
		verify, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		outcome, lookupErr := repo.CombatAttackOutcome(verify, s.currentLease(), t.request.OperationID, t.hash)
		cancel()
		if lookupErr == nil {
			if outcome == nil {
				unlock()
				s.releaseCombatClaims(t)
				return s.rejectCombatTicket(t)
			}
			s.commitCombatTicket(t, *outcome)
			s.publishCombatProgressLocked(t, *outcome)
			unlock()
			s.publishCombatTicket(ctx, t, *outcome)
			return nil
		}
		// Publish the uncertainty gate BEFORE releasing any peer persistMu.
		// A later transient vital command must not observe an unguarded window.
		g := s.gameplay
		g.mu.Lock()
		if g.pendingCombat == nil {
			g.pendingCombat = map[string]*combatTicket{}
		}
		g.pendingCombat[t.request.OperationID] = t
		g.mu.Unlock()
		unlock()
		return t.sink.WritePacket(protocol.EnableActions())
	}
	s.releaseCombatClaims(t)
	return s.rejectCombatTicket(t)
}

// combatVitalsPending is called with live.persistMu held. An unresolved receipt
// freezes NEW transient HP/MP commands for every frozen candidate, not just the
// caster. Commands are rejected (not queued or replayed); they must not consume
// regeneration/damage throttles. Durable skill commands independently enforce
// the frozen character revision. No registry mutex is held over SQL or writes.
func (s *Server) combatVitalsPending(live *liveCharacter, session domain.CharacterSession) bool {
	if assetOutcomeUncertain(live, session) != nil {
		return true
	}
	if s.partySkillVitalsPending(live, session) {
		return true
	}
	if s.gameplay == nil {
		return false
	}
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	for _, ticket := range s.gameplay.pendingCombat {
		for _, peer := range ticket.peers {
			if peer.live == live && peer.session.CharacterID == session.CharacterID &&
				peer.session.SessionID == session.SessionID && peer.session.Generation == session.Generation {
				return true
			}
		}
	}
	return false
}

func (s *Server) commitCombatTicket(t *combatTicket, result domain.CombatAttackResult) {
	// Caller owns every candidate persistMu, both initially and in the resolver.
	for _, p := range t.peers {
		for _, value := range result.Characters {
			if value.ID != p.session.CharacterID {
				continue
			}
			g := s.gameplay
			g.mu.Lock()
			room := g.rooms[t.key]
			registered := room != nil && room.members[value.ID] == p.entry && p.entry.live == p.live && sameDropSink(p.entry.sink, p.sink)
			g.mu.Unlock()
			if !registered {
				continue
			}
			p.live.mu.Lock()
			session := &p.live.session
			if session.SessionID == p.session.SessionID && session.Generation == p.session.Generation && session.ChannelID == t.key.channel && session.Character.MapID == t.key.mapID && session.Character.Revision < value.Revision {
				c := &session.Character
				c.EXP, c.Level, c.STR, c.DEX, c.INT, c.LUK, c.AP, c.SP = value.EXP, value.Level, value.STR, value.DEX, value.INT, value.LUK, value.AP, value.SP
				c.HP, c.MP, c.MaxHP, c.MaxMP = value.HP, value.MP, value.MaxHP, value.MaxMP
				c.BaseMaxHP, c.BaseMaxMP, c.VitalsModel, c.VitalsOrigin = value.BaseMaxHP, value.BaseMaxMP, value.VitalsModel, value.VitalsOrigin
				c.Revision = value.Revision
				c.Quests = cloneCombatSession(domain.CharacterSession{Character: value}).Character.Quests
				mergeCommittedCombatCooldown(t, c, value, time.Now())
			}
			snapshot := cloneCombatSession(*session)
			p.live.mu.Unlock()
			s.mu.Lock()
			if prior, ok := s.online[value.ID]; ok && prior.SessionID == snapshot.SessionID && prior.Generation == snapshot.Generation {
				s.online[value.ID] = snapshot
			}
			s.mu.Unlock()
		}
	}
	if result.Ammo != nil {
		t.actor.mu.Lock()
		if cur := &t.actor.session; cur.SessionID == t.actorSession.SessionID && cur.Generation == t.actorSession.Generation {
			items := cur.Character.Items[:0]
			for _, it := range cur.Character.Items {
				if it.InvType == 2 && it.Slot == result.Ammo.Slot && it.TemplateID == result.Ammo.ItemID {
					if result.Ammo.Quantity == 0 {
						continue
					}
					it.Quantity = result.Ammo.Quantity
				}
				items = append(items, it)
			}
			cur.Character.Items = items
		}
		t.actor.mu.Unlock()
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	t.observers = combatRoomPeers(g.rooms[t.key])
	for i, mob := range t.targets {
		if mob.reservation != t {
			continue
		}
		q := t.request.Targets[i]
		mob.hp = q.BeforeHP - int64(q.Damage)
		t.recordAppliedDamageLocked(q, mob.hp, true)
		mob.contributions = append([]domain.CombatContribution(nil), q.Contributions...)
		mob.claiming = false
		mob.reservation = nil
		if q.Defeated {
			mob.respawnAt = mob.respawnTime(t.key.mapID, time.Now())
			mob.statuses = nil
		}
	}
	s.applyGenericStatusesLocked(t, time.Now())
	delete(g.pendingCombat, t.request.OperationID)
}

// Caller holds the sorted recipient persistence locks through progress delivery,
// just as a map transfer holds its lock through SetField publication.
func (s *Server) publishCombatProgressLocked(t *combatTicket, result domain.CombatAttackResult) {
	for _, p := range t.peers {
		s.gameplay.mu.Lock()
		room := s.gameplay.rooms[t.key]
		registered := room != nil && room.members[p.session.CharacterID] == p.entry && p.entry.live == p.live && sameDropSink(p.entry.sink, p.sink)
		s.gameplay.mu.Unlock()
		if !registered {
			continue
		}
		p.live.mu.Lock()
		snapshot := cloneCombatSession(p.live.session)
		p.live.mu.Unlock()
		if snapshot.SessionID != p.session.SessionID || snapshot.Generation != p.session.Generation || snapshot.ChannelID != t.key.channel || snapshot.Character.MapID != t.key.mapID {
			continue
		}
		changed := false
		for _, c := range result.Characters {
			changed = changed || c.ID == snapshot.CharacterID
		}
		if !changed {
			continue
		}
		s.publishCommittedCombatCooldown(t, p, snapshot)
		_ = p.sink.WritePacket(protocol.UpdateCombatStats(snapshot.Character))
		publishCombatGainNotifications(t, p, snapshot.Character, result)
		for _, state := range snapshot.Character.Quests {
			for _, prior := range p.session.Character.Quests {
				if state.ID == prior.ID && state.State == 1 && state.Revision != prior.Revision {
					_ = p.sink.WritePacket(protocol.QuestMobUpdate(state))
				}
			}
		}
		s.refreshParty(p.live)
	}
}

// combatRoomPeers is called only under gameplay.mu. Entry pointers distinguish
// two visits to the same room even when the live aggregate and sink persist.
func combatRoomPeers(room *gameplayRoom) []combatLivePeer {
	var out []combatLivePeer
	if room != nil {
		for id, m := range room.members {
			if m.live != nil {
				out = append(out, combatLivePeer{id: id, live: m.live, sink: m.sink, entry: m})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (s *Server) publishCombatTicket(ctx context.Context, t *combatTicket, result domain.CombatAttackResult) {
	t.publishAppliedDamageEvidence()
	if t.generic != nil && t.generic.then != nil {
		defer t.generic.then()
	}
	if t.generic != nil && attackAppliedDiag.Add(1) <= 500 {
		var claimed, applied int64
		killed := 0
		for _, q := range t.request.Targets {
			applied += int64(q.Damage)
			for _, l := range q.Lines {
				claimed += l.Claimed() // bit31 is the critical flag, not damage
			}
			if q.Defeated {
				killed++
			}
		}
		slog.Info("attack_applied", "character", t.request.Session.CharacterID, "skill", t.request.SkillID, "family", t.request.Family, "targets", len(t.request.Targets), "lines", t.request.LinesPerTarget, "claimed", claimed, "applied", applied, "killed", killed, "clipped", t.generic.clipped, "below_floor", t.generic.belowFloor, "summon", t.generic.summon != nil, "spawns", len(result.Spawns))
	}
	long := protocol.NewLongDamageOut()
	var attack []byte
	switch {
	case t.generic != nil:
		attack = t.generic.remotePacket(t.request.Session.Character, long)
	case t.magic != nil:
		attack = protocol.MagicAttackPacket(t.request.Session.Character, t.magicLevel, *t.magic)
	default:
		attack = protocol.MeleeAttackPacket(t.request.Session.Character, t.attack)
	}
	if result.Ammo != nil {
		_ = t.sink.WritePacket(protocol.UpdateUseSlot(result.Ammo.Slot, result.Ammo.Quantity))
	}
	for i, id := range t.request.MesoDrops {
		s.broadcastInventoryPacket(t.key.channel, t.key.mapID, protocol.RemoveItemExploded(id, 100*i))
	}
	damageFrames := make([][]byte, len(t.request.Targets))
	for i, q := range t.request.Targets {
		damageFrames[i] = protocol.DamageMonsterLong(q.ObjectID, q.Damage, long)
	}
	longFrame := long.Packet()
	for _, receiver := range t.observers {
		// No other recipient lock is held here: an unrelated observer can be the
		// actor of another settlement without introducing a reversed lock order.
		receiver.live.persistMu.Lock()
		receiver.live.mu.Lock()
		current := receiver.live.session
		receiver.live.mu.Unlock()
		s.gameplay.mu.Lock()
		room := s.gameplay.rooms[t.key]
		valid := room != nil && room.members[receiver.id] == receiver.entry && receiver.entry.live == receiver.live && sameDropSink(receiver.entry.sink, receiver.sink)
		s.gameplay.mu.Unlock()
		if !valid || current.CharacterID != receiver.id || current.SessionID != receiver.entry.sessionID || current.Generation != receiver.entry.generation || current.Lease.WorldID != receiver.entry.worldID || current.Lease.WorldID != t.actorSession.Lease.WorldID || current.ChannelID != t.key.channel || current.Character.MapID != t.key.mapID {
			receiver.live.persistMu.Unlock()
			continue
		}
		sink := receiver.sink
		if longFrame != nil {
			_ = sink.WritePacket(longFrame)
		}
		if attack != nil && (t.request.Family == domain.CombatNormal || receiver.id != t.request.Session.CharacterID) {
			_ = sink.WritePacket(attack)
		}
		for i, q := range t.request.Targets {
			_ = sink.WritePacket(damageFrames[i])
			hp := q.BeforeHP - int64(q.Damage)
			var maximum int64
			if i < len(t.targets) {
				maximum = t.targets[i].stats.HP // Immutable template, not mutable respawn object ID.
				if t.targets[i].prelude {
					hp = maximum
				}
			}
			_ = sink.WritePacket(protocol.MonsterHP(q.ObjectID, hp, maximum))
			if q.Defeated {
				_ = sink.WritePacket(protocol.KillMonster(q.ObjectID))
			}
		}
		if t.generic != nil {
			for _, p := range t.generic.statusOut {
				_ = sink.WritePacket(p)
			}
			for _, p := range t.generic.extraOut {
				_ = sink.WritePacket(p)
			}
		}
		receiver.live.persistMu.Unlock()
	}
	for _, q := range t.request.Targets {
		if q.Defeated {
			_ = s.spawnCombatLoot(ctx, t.actor, t.sink, q.SpawnOperationID, q.MobID, q.X, q.Y)
			s.expeditionBossDefeated(t.key, t.actorSession.CharacterID, q.MobID)
		}
	}
	if t.generic != nil {
		e := combat.AttackEvent{Session: t.actorSession, Channel: t.key.channel, SkillID: t.request.SkillID, Kind: combat.KindForOpcode(t.generic.attack.Opcode), Summon: t.generic.summon != nil, Now: time.Now()}
		if m := t.generic.summon; m != nil {
			e.SummonSkillID = m.skillID
		}
		e.SpawnMesos = s.attackMesoSpawner(ctx, t)
		e.Lines = t.request.LinesPerTarget
		e.StealItem = s.attackStealer(ctx, t)
		e.SpawnSummon = func(skillID, level int) error {
			_, err := s.SpawnSummon(t.key.channel, t.actor, skillID, level, time.Now())
			return err
		}
		e.SummonCount = func(skillID int) int { return s.summonCount(t.key, t.actorSession.CharacterID, skillID) }
		e.GainForce = func(objectID int32, count, color int) error {
			return t.sink.WritePacket(protocol.GainForce(objectID, []protocol.ForceGain{{Count: int32(count), Color: int32(color)}}))
		}
		for i, q := range t.request.Targets {
			if q.Damage > 0 {
				e.Targets = append(e.Targets, q.ObjectID)
				e.Damage += int64(q.Damage)
				h := combat.HitInfo{ObjectID: q.ObjectID, MobID: q.MobID, SpawnOperationID: q.SpawnOperationID, X: q.X, Y: q.Y}
				if i < len(t.targets) {
					h.Boss = t.targets[i].stats.Boss
				}
				if i < len(t.generic.attack.Targets) {
					for _, l := range t.generic.attack.Targets[i].Lines {
						h.Lines = append(h.Lines, l.Damage)
					}
				}
				e.Hits = append(e.Hits, h)
			}
			if q.Defeated {
				e.Defeated = append(e.Defeated, q.ObjectID)
			}
		}
		combat.PublishAttack(e)
		if t.generic.spec != nil && t.generic.spec.AfterHit != nil && len(e.Targets) > 0 {
			t.generic.spec.AfterHit(e)
		}
		// v145 places Poison Mist through the magic attack (Java MagicDamage
		// then effect.applyTo(pos)), so field skills spawn after the attack.
		if spec := t.generic.spec; spec != nil && spec.Field != nil && t.generic.summon == nil && !t.generic.serverDamage {
			_, _ = s.SpawnFieldObject(t.key.channel, t.actor, t.request.SkillID, t.generic.level, e.Now)
		}
	}
}

func (s *Server) runCombatSettlements(ctx context.Context) {
	repo, ok := s.repo.(domain.CombatSettlementRepository)
	if !ok || s.gameplay == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.resolveCombatSettlements(ctx, repo)
		}
	}
}

func (s *Server) resolveCombatSettlements(ctx context.Context, repo domain.CombatSettlementRepository) {
	g := s.gameplay
	g.mu.Lock()
	var pending []*combatTicket
	for _, t := range g.pendingCombat {
		if !t.resolving {
			t.resolving = true
			pending = append(pending, t)
		}
	}
	g.mu.Unlock()
	for _, t := range pending {
		step, cancel := context.WithTimeout(ctx, 2*time.Second)
		result, err := repo.CombatAttackOutcome(step, s.currentLease(), t.request.OperationID, t.hash)
		cancel()
		if err != nil {
			g.mu.Lock()
			t.resolving = false
			g.mu.Unlock()
			continue
		}
		if result == nil {
			s.releaseCombatClaims(t)
			_ = s.rejectCombatTicket(t)
			continue
		}
		// The original sorted peer list is immutable. Disconnected/replaced peers
		// are checked again by commitCombatTicket and remain durable-only rewards.
		for _, p := range t.peers {
			p.live.persistMu.Lock()
		}
		s.commitCombatTicket(t, *result)
		s.publishCombatProgressLocked(t, *result)
		for i := len(t.peers) - 1; i >= 0; i-- {
			t.peers[i].live.persistMu.Unlock()
		}
		s.publishCombatTicket(ctx, t, *result)
	}
}
