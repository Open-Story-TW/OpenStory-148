package world

import (
	"context"
	"fmt"
	"log/slog"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"os"
	"sort"
	"time"
)

type partySkillPeer struct {
	member   *skillMember
	visit    *sceneVisit
	session  domain.CharacterSession
	audience []*skillMember // transient FX audience captured before SQL
}
type partySkillTicket struct {
	request    domain.PartySkillCastRequest
	plan       skills.CastPlan // caster-side plan used to pick recipients
	hash       string
	peers      []partySkillPeer // immutable ascendingCID lock order
	source     *skillMember
	auraSource int64 // source on the toggle path, zero on a legacy row
	resolving  bool  // skillSpace.mu only
}

func (s *Server) partySkillVitalsPending(live *liveCharacter, session domain.CharacterSession) bool {
	if s.skills == nil {
		return false
	}
	s.skills.mu.Lock()
	defer s.skills.mu.Unlock()
	for _, ticket := range s.skills.partyPending {
		for _, p := range ticket.peers {
			if p.member.live == live && sameSceneOwner(p.session, session) {
				return true
			}
		}
	}
	return false
}
func (s *Server) forgetPartySkill(t *partySkillTicket) {
	s.skills.mu.Lock()
	if s.skills.partyPending[t.request.OperationID] == t {
		delete(s.skills.partyPending, t.request.OperationID)
	}
	s.skills.mu.Unlock()
}
func lockPartySkillPeers(peers []partySkillPeer) func() {
	for _, p := range peers {
		p.member.live.persistMu.Lock()
	}
	return func() {
		for i := len(peers) - 1; i >= 0; i-- {
			peers[i].member.live.persistMu.Unlock()
		}
	}
}
func (s *Server) rejectPartySkill(t *partySkillTicket) error {
	for _, p := range t.peers {
		if p.member == t.source {
			p.member.live.mu.Lock()
			c := p.member.live.session.Character
			p.member.live.mu.Unlock()
			return s.writeScenePackets(p.visit, protocol.UpdateHPMP(c.HP, c.MP), protocol.EnableActions())
		}
	}
	return nil
}
func (s *Server) castPartySkill(ctx context.Context, live *liveCharacter, sink packetSink, q protocol.SkillCast) error {
	return s.applyPartySkill(ctx, live, sink, q, false)
}

func (s *Server) cancelBlueAura(ctx context.Context, live *liveCharacter, sink packetSink) error {
	return s.applyPartySkill(ctx, live, sink, protocol.SkillCast{SkillID: skills.BlueAuraSkill}, true)
}

func (s *Server) applyPartySkill(ctx context.Context, live *liveCharacter, sink packetSink, q protocol.SkillCast, cancelAura bool) error {
	repo, ok := s.repo.(domain.PartySkillRepository)
	partyRepo, partyOK := s.repo.(domain.PartyRepository)
	// Only mass spells (native predicate 0x669D0E) carry a position; Time
	// Leap and other party skills use the server's position.
	if !ok || !partyOK || s.skills == nil || !cancelAura && s.tradeActive(live) {
		if cancelAura {
			return s.cancelSkill(ctx, live, sink, q.SkillID)
		}
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	initial := cloneCombatSession(s.skillSession(live))
	source := s.skillMemberFor(initial, live, sink)
	visit := s.sceneFor(live, sink)
	blocked := s.combatVitalsPending(live, initial)
	aura, _ := skills.ActiveBlueAura(initial.Character, time.Now())
	if cancelAura && aura.SourceCharacterID != initial.CharacterID {
		defer live.persistMu.Unlock()
		return s.cancelSkillLocked(ctx, live, sink, q.SkillID)
	}
	live.persistMu.Unlock()
	if source == nil || visit == nil || blocked {
		slog.Info("party_skill_rejected", "character", initial.CharacterID, "skill", q.SkillID, "reason", "not_ready",
			"source", source != nil, "scene", visit != nil, "vitals_pending", blocked)
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	step, cancel := context.WithTimeout(ctx, 3*time.Second)
	party, e := partyRepo.ReadParty(step, initial)
	cancel()
	if e != nil {
		slog.Info("party_skill_rejected", "character", initial.CharacterID, "skill", q.SkillID, "reason", "party_read", "error", e)
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	ids := map[int64]bool{initial.CharacterID: true}
	if party.Party.ID != 0 {
		if _, member := party.Party.Member(initial.CharacterID); !member {
			slog.Info("party_skill_rejected", "character", initial.CharacterID, "skill", q.SkillID, "reason", "not_party_member")
			return s.writeScenePackets(visit, protocol.EnableActions())
		}
		for _, p := range party.Party.Members {
			ids[p.CharacterID] = true
		}
	}
	if len(ids) > domain.PartyCapacity {
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	s.skills.mu.Lock()
	var candidates []partySkillPeer
	for id := range ids {
		if m := s.skills.members[id]; m != nil && m.worldID == source.worldID && m.channel == source.channel && m.mapID == source.mapID {
			candidates = append(candidates, partySkillPeer{member: m})
		}
	}
	s.skills.mu.Unlock()
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].member.characterID < candidates[j].member.characterID })
	seenLive := map[*liveCharacter]bool{}
	for _, p := range candidates {
		if seenLive[p.member.live] {
			return s.writeScenePackets(visit, protocol.EnableActions())
		}
		seenLive[p.member.live] = true
	}
	unlock := lockPartySkillPeers(candidates)
	defer unlock()
	current := cloneCombatSession(s.skillSession(live))
	if s.skillMemberFor(current, live, sink) != source || s.sceneFor(live, sink) != visit || !sameSceneOwner(initial, current) || s.combatVitalsPending(live, current) {
		slog.Info("party_skill_rejected", "character", current.CharacterID, "skill", q.SkillID, "reason", "stale_or_pending")
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	var plan skills.CastPlan
	if cancelAura {
		found, ok := skills.ActiveBlueAura(current.Character, time.Now())
		if !ok || found.SourceCharacterID != current.CharacterID || !found.ExpiresAt.Equal(aura.ExpiresAt) {
			return s.writeScenePackets(visit, protocol.EnableActions())
		}
	} else {
		plan, e = skills.PartyCast(os.Getenv("MAPLEGO_WZ_ROOT"), current.Character, q.SkillID, q.Level, time.Now())
		if e != nil {
			slog.Info("party_skill_rejected", "character", current.CharacterID, "skill", q.SkillID, "error", e)
			return s.writeScenePackets(visit, protocol.UpdateHPMP(current.Character.HP, current.Character.MP), protocol.EnableActions())
		}
	}
	// Maple Warrior's mass-spell position is advisory. Java applies it using
	// the current server character position; this handler likewise selects
	// every recipient from authoritative coordinates, never the supplied XY.
	// A stale client position must not veto this self/party stat buff.
	mapleWarrior := plan.Party != nil && len(plan.Party.Stats) == 1 && plan.Party.Stats[0].Stat == "MAPLE_WARRIOR"
	if q.HasPosition && !mapleWarrior && !within(current.Character.X, current.Character.Y, int(q.X), int(q.Y), 64) {
		slog.Info("party_skill_rejected", "character", current.CharacterID, "skill", q.SkillID, "reason", "position", "server_x", current.Character.X, "server_y", current.Character.Y, "wire_x", q.X, "wire_y", q.Y)
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	maxima := false
	if plan.Party != nil {
		for _, v := range plan.Party.Stats {
			maxima = maxima || v.Stat == "MAXHP" || v.Stat == "MAXMP"
		}
	}
	// A toggle recast removes the skill from every same-map party member,
	// wherever they stand.
	toggleOff := cancelAura || skills.PartyToggleActive(plan, current.Character, q.SkillID, time.Now())
	t := &partySkillTicket{source: source, plan: plan, request: domain.PartySkillCastRequest{Session: current, OperationID: fmt.Sprintf("party-skill-%s-%d-%d", current.SessionID, current.Generation, q.Tick), ClientTick: q.Tick, SkillID: q.SkillID, ClientLevel: q.Level, PartyID: party.Party.ID, PartyRevision: party.Party.Revision}}
	if q.SkillID == skills.BlueAuraSkill {
		currentAura, _ := skills.ActiveBlueAura(current.Character, time.Now())
		t.auraSource = currentAura.SourceCharacterID
	}
	if cancelAura {
		t.request.CancelAuraExpiry = &aura.ExpiresAt
		t.request.OperationID = fmt.Sprintf("blue-aura-cancel-%s-%d-%d", current.SessionID, current.Generation, aura.ExpiresAt.UnixNano())
	}
	for _, p := range party.Party.Members {
		t.request.PartyMembers = append(t.request.PartyMembers, p.CharacterID)
	}
	for _, candidate := range candidates {
		m := candidate.member
		snapshot := cloneCombatSession(s.skillSession(m.live))
		if s.skillMemberFor(snapshot, m.live, m.sink) != m || m == source && snapshot.Character.HP <= 0 || !toggleOff && (maxima && snapshot.Character.VitalsModel != 1 || m != source && !skills.PartyRecipient(plan, current.Character, snapshot.Character)) {
			continue
		}
		pin := s.sceneFor(m.live, m.sink)
		if pin == nil || s.combatVitalsPending(m.live, snapshot) {
			slog.Info("party_skill_rejected", "character", current.CharacterID, "skill", q.SkillID, "reason", "recipient_not_ready", "recipient", snapshot.CharacterID)
			return s.writeScenePackets(visit, protocol.EnableActions())
		}
		candidate.session, candidate.visit, candidate.audience = snapshot, pin, s.skillCastAudience(m)
		t.peers = append(t.peers, candidate)
		t.request.Members = append(t.request.Members, snapshot)
	}
	foundSource := false
	for _, p := range t.peers {
		foundSource = foundSource || p.member == source
	}
	if !foundSource {
		slog.Info("party_skill_rejected", "character", current.CharacterID, "skill", q.SkillID, "reason", "caster_not_recipient")
		return s.writeScenePackets(visit, protocol.EnableActions())
	}
	t.hash, e = domain.PartySkillManifestHash(t.request)
	if e != nil {
		return e
	}
	step, cancel = context.WithTimeout(ctx, 3*time.Second)
	result, e := repo.ApplyPartySkillCast(step, t.request)
	cancel()
	if e != nil {
		if combatKnownFailure(e) {
			slog.Info("party_skill_rejected", "character", current.CharacterID, "skill", q.SkillID, "reason", "repository", "error", e)
			return s.rejectPartySkill(t)
		}
		verify, done := context.WithTimeout(context.Background(), 2*time.Second)
		out, lookupErr := repo.PartySkillCastOutcome(verify, s.currentLease(), t.request.OperationID, t.hash)
		done()
		if lookupErr == nil {
			if out == nil {
				return s.rejectPartySkill(t)
			}
			result = *out
		} else {
			// Registration precedes deferred sorted-peer unlock, closing the same
			// lost-vitals window as the shared-combat uncertainty gate.
			s.skills.mu.Lock()
			if s.skills.partyPending == nil {
				s.skills.partyPending = map[string]*partySkillTicket{}
			}
			s.skills.partyPending[t.request.OperationID] = t
			s.skills.mu.Unlock()
			return s.writeScenePackets(visit, protocol.EnableActions())
		}
	}
	err := s.commitPartySkill(t, result)
	for _, p := range t.peers {
		s.refreshParty(p.member.live)
	}
	return err
}

// Validate the entire immutable result before changing any live recipient.
// A malformed second recipient must not leave an already-mutated first peer.
func validPartySkillResult(t *partySkillTicket, r domain.PartySkillCastResult) bool {
	if r.OperationID != t.request.OperationID || r.ManifestHash != t.hash || len(r.Recipients) != len(t.peers) ||
		t.request.CancelAuraExpiry != nil && !r.ToggledOff {
		return false
	}
	expected := map[int64]partySkillPeer{}
	for _, p := range t.peers {
		expected[p.session.CharacterID] = p
	}
	seen := map[int64]bool{}
	applied, level := skills.PartyBuffSource(t.plan, t.request.SkillID, t.request.ClientLevel)
	blueAura := t.request.SkillID == skills.BlueAuraSkill
	for _, v := range r.Recipients {
		p, ok := expected[v.Character.ID]
		if !ok || seen[v.Character.ID] {
			return false
		}
		seen[v.Character.ID] = true
		a, b := v.Character, p.session.Character
		if !sameSceneOwner(v.Session, p.session) || v.Session.Character.MapID != b.MapID || v.Session.Character.Revision != b.Revision || a.Revision != b.Revision+1 || a.AccountID != p.session.AccountID || a.WorldID != p.session.Lease.WorldID || a.MapID != b.MapID || a.Job != b.Job || a.Level != b.Level || a.EXP != b.EXP || a.AP != b.AP || a.SP != b.SP || a.BaseMaxHP != b.BaseMaxHP || a.BaseMaxMP != b.BaseMaxMP || a.VitalsModel != 1 || a.HP < 0 || a.MP < 0 || a.HP > a.MaxHP || a.MP > a.MaxMP {
			return false
		}
		// Every recipient carries exactly the planned stats of this skill,
		// at the cast level, sharing one expiry (none after a toggle-off).
		want := map[string]int{}
		if !r.ToggledOff {
			want = plannedBuffStats(t.plan.Party.Stats)
		}
		var expiry time.Time
		got := 0
		for _, buff := range a.SkillBuffs {
			if r.ToggledOff && blueAura {
				if skills.IsBlueAuraRow(buff) && buff.SourceCharacterID == t.auraSource {
					return false
				}
				continue
			}
			if buff.ID != applied {
				continue
			}
			value, ok := want[buff.Stat]
			if !ok || buff.Level != level || buff.Value != value || got > 0 && !buff.ExpiresAt.Equal(expiry) ||
				blueAura && buff.SourceCharacterID != t.source.characterID {
				return false
			}
			expiry = buff.ExpiresAt
			got++
		}
		if got != len(want) {
			return false
		}
	}
	return true
}

// Caller owns sorted peer persistMu. Acquire ascendingCID gates, then the
// existing map/skill publication boundary. Never perform SQL or wait persistMu
// under a scene/publication/registry lock. Stale results remain durable-only.
func (s *Server) commitPartySkill(t *partySkillTicket, result domain.PartySkillCastResult) error {
	if !validPartySkillResult(t, result) {
		slog.Warn("party_skill_rejected", "character", t.request.Session.CharacterID, "skill", t.request.SkillID, "reason", "invalid_repository_result")
		return domain.ErrForbidden
	}
	type committedPeer struct {
		peer    partySkillPeer
		session domain.CharacterSession
	}
	// Prepare every receipt row before any live/online mutation. One bad
	// later recipient cannot leave earlier peers partially reconciled.
	derived := map[int64]domain.Character{}
	normalized := map[int64]bool{}
	deriveAt := time.Now()
	for _, row := range result.Recipients {
		c := cloneCombatSession(domain.CharacterSession{Character: row.Character}).Character
		hp, mp, e := content.DerivedVitals(c, deriveAt)
		if e != nil {
			return domain.ErrInvalidInput
		}
		c.MaxHP, c.MaxMP = hp, mp
		c.HP, c.MP = min(c.HP, hp), min(c.MP, mp)
		derived[c.ID] = c
		normalized[c.ID] = c.HP != row.Character.HP || c.MP != row.Character.MP
	}
	var gateUnlocks []func()
	for _, p := range t.peers {
		release, e := s.lockScenePublication(p.session.CharacterID)
		if e != nil {
			for i := len(gateUnlocks) - 1; i >= 0; i-- {
				gateUnlocks[i]()
			}
			return e
		}
		gateUnlocks = append(gateUnlocks, release)
	}
	defer func() {
		for i := len(gateUnlocks) - 1; i >= 0; i-- {
			gateUnlocks[i]()
		}
	}()
	publication := s.lockSkillPublication()
	defer publication()
	var committed []committedPeer
	for _, p := range t.peers {
		if !s.sceneCurrent(p.visit) {
			continue
		}
		current, valid := s.skillCurrent(p.member)
		if !valid || !sameSceneOwner(current, p.session) || current.Character.Revision != p.session.Character.Revision {
			continue
		}
		for _, r := range result.Recipients {
			if r.Character.ID != p.session.CharacterID {
				continue
			}
			if !sameSceneOwner(r.Session, p.session) || r.Session.Character.MapID != p.session.Character.MapID || r.Session.Character.Revision != p.session.Character.Revision || r.Character.Revision != p.session.Character.Revision+1 || r.Character.AccountID != p.session.AccountID || r.Character.WorldID != p.session.Lease.WorldID || r.Character.MapID != p.session.Character.MapID {
				return domain.ErrForbidden
			}
			next := cloneCombatSession(current)
			next.Character = derived[r.Character.ID]
			next.Character.X, next.Character.Y, next.Character.SpawnPoint = current.Character.X, current.Character.Y, current.Character.SpawnPoint
			p.member.live.mu.Lock()
			if p.member.live.session.Character.Revision != current.Character.Revision {
				p.member.live.mu.Unlock()
				continue
			}
			if !s.updateOnlineOwned(current, next) {
				p.member.live.mu.Unlock()
				continue
			}
			p.member.live.session = next
			if normalized[r.Character.ID] {
				p.member.live.dirty = true
			}
			p.member.live.mu.Unlock()
			committed = append(committed, committedPeer{p, next})
			break
		}
	}
	s.forgetPartySkill(t)
	now := time.Now()
	var sourceErr error
	skillID := t.request.SkillID
	applied, _ := skills.PartyBuffSource(t.plan, skillID, t.request.ClientLevel)
	for _, entry := range committed {
		p, c := entry.peer, entry.session.Character
		var buffs []domain.SkillBuff
		for _, b := range c.SkillBuffs {
			if b.ID == applied && b.ExpiresAt.After(now) {
				buffs = append(buffs, b)
			}
		}
		peers := s.skillPeers(c.ID, p.member.channel, p.member.mapID)
		for _, row := range result.Recipients {
			if row.Character.ID == c.ID {
				s.publishSkillExpiry(p.member, p.member.sink, peers, c, row.Expired, now)
			}
		}
		for _, row := range result.Recipients {
			if row.Character.ID == c.ID {
				if len(row.Inventory) > 0 {
					_ = p.member.sink.WritePacket(protocol.ModifyInventory(row.Inventory))
				}
				s.publishResetCooldowns(p.member, row.ResetCooldowns)
				s.publishUpdatedRows(p.member, peers, c, row.Updated, now)
			}
		}
		// A long-delayed receipt still reconciles committed cost/revision; expired
		// effects do not emit an already-expired temporary-state or cast flash.
		if !result.ToggledOff && len(buffs) == len(plannedBuffStats(t.plan.Party.Stats)) {
			if len(buffs) > 0 {
				if e := p.member.sink.WritePacket(protocol.SkillBuffsFor(c, buffs, now)); e != nil && p.member == t.source {
					sourceErr = e
				}
				for _, target := range peers {
					if s.skillVisible(p.member, target) {
						_ = target.sink.WritePacket(protocol.ForeignSkillBuffs(c.ID, buffs))
					}
				}
			}
			if p.member != t.source {
				_ = p.member.sink.WritePacket(protocol.SkillAffectedEffect(0, skillID, t.request.ClientLevel))
				for _, target := range p.audience {
					if s.skillVisible(p.member, target) {
						_ = target.sink.WritePacket(protocol.SkillAffectedEffect(c.ID, skillID, t.request.ClientLevel))
					}
				}
			} else {
				for _, target := range p.audience {
					if s.skillVisible(p.member, target) {
						_ = target.sink.WritePacket(protocol.SkillEffect(c.ID, skillID, c.Level, t.request.ClientLevel))
					}
				}
			}
		}
		if e := p.member.sink.WritePacket(protocol.InventoryVitals(c)); e != nil && p.member == t.source {
			sourceErr = e
		}
	}
	// refreshParty performs SQL; callers run it after this publication scope.
	return sourceErr
}
func (s *Server) resolvePartySkills(ctx context.Context, repo domain.PartySkillRepository) {
	if s.skills == nil {
		return
	}
	s.skills.mu.Lock()
	var pending []*partySkillTicket
	for _, t := range s.skills.partyPending {
		if !t.resolving {
			t.resolving = true
			pending = append(pending, t)
		}
	}
	s.skills.mu.Unlock()
	for _, t := range pending {
		step, cancel := context.WithTimeout(ctx, 2*time.Second)
		out, e := repo.PartySkillCastOutcome(step, s.currentLease(), t.request.OperationID, t.hash)
		cancel()
		if e != nil {
			s.skills.mu.Lock()
			t.resolving = false
			s.skills.mu.Unlock()
			continue
		}
		unlock := lockPartySkillPeers(t.peers)
		if out == nil {
			s.forgetPartySkill(t)
			_ = s.rejectPartySkill(t)
		} else {
			if e = s.commitPartySkill(t, *out); e != nil {
				s.skills.mu.Lock()
				t.resolving = false
				s.skills.mu.Unlock()
			}
		}
		for _, p := range t.peers {
			s.refreshParty(p.member.live)
		}
		unlock()
	}
}

// plannedBuffStats is the stat-to-value map a buff's planned stats are saved
// as: with MAPLEGO_STACK_BUFFS on, additive stats are stored under their indie
// form and a stat the plan carries twice is merged, as persistence does.
func plannedBuffStats(stats []skills.StatValue) map[string]int {
	out := make(map[string]int, len(stats))
	stack := domain.StackBuffsEnabled()
	for _, v := range stats {
		name := v.Stat
		if stack {
			name = buffstat.StackedForm(name)
			out[string(name)] += v.Value
			continue
		}
		out[string(name)] = v.Value
	}
	return out
}
