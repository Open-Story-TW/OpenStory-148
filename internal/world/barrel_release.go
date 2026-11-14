package world

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Only the outgoing 0xAB release key is reviewed: native CGrenade constructor
// 0x83CAFE assigns ++D255D0 to +28, which 0x86E531 serializes. No key is inferred
// from the protected 0x4B encoder. Collision admission/fees remain unchanged
// until that separate causal link has primary evidence and MCP coverage.
type barrelReleaseTicket struct {
	key       int32
	wireHash  [32]byte
	entry     *gameplayMember
	visit     *sceneVisit
	combat    *combatTicket
	paid      bool
	finished  bool // Known completion; false also covers an unresolved receipt.
	executing bool // Before the first Apply/Outcome attempt has returned.
}

func (s *Server) releaseBarrel(ctx context.Context, channel int, live *liveCharacter, sink packetSink, q protocol.ThrowGrenade, wire []byte) error {
	repo, ok := s.repo.(domain.CombatSettlementRepository)
	if !ok || s.gameplay == nil || q.SkillID != 5301001 || q.Key <= 0 || len(wire) != 29 {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	live.mu.Lock()
	session := cloneCombatSession(live.session)
	live.mu.Unlock()
	session.Lease = s.currentLease()
	c := session.Character
	v := s.sceneFor(live, sink)
	if v == nil || c.HP <= 0 || session.ChannelID != channel || s.tradeActive(live) || s.combatVitalsPending(live, session) {
		live.persistMu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	// Native places this barrel 40px in front of the actor and corrects its
	// ground within 35px (0x86E37D..0x86E3B5). Retain the ordinary position
	// tolerance for independently arriving movement; no landing is invented.
	x := c.X + 40
	if q.FacingLeft {
		x = c.X - 40
	}
	dy := c.Y - int(q.CharacterY)
	if !within(x, c.Y, int(q.X), int(q.Y), 120) || max(dy, -dy) > 120 {
		live.persistMu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	now := time.Now()
	a := protocol.Attack{Opcode: protocol.RecvMeleeAttack, SkillID: q.SkillID, TByte: 1, X: int16(c.X), Y: int16(c.Y)}
	if q.FacingLeft {
		a.Display = 0x8000
	}
	spec, effect, level, learned := resolveAttackEffect(c, &a, now)
	definition, definitionErr := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), q.SkillID)
	stats, err := combat.AttackStats(c, now)
	if !learned || err != nil || definitionErr != nil || !definition.CanLearnBy(c) {
		live.persistMu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	plan, err := combat.PlanAttack(spec, combat.AttackContext{Kind: combat.Melee, Character: c, Stats: stats, Effect: effect, Attack: a, Now: now}, rollProp)
	if err != nil {
		live.persistMu.Unlock()
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	gate, err := s.lockScenePublication(c.ID)
	if err != nil {
		live.persistMu.Unlock()
		return err
	}
	if !s.sceneCurrent(v) {
		gate()
		live.persistMu.Unlock()
		return domain.ErrStaleOwner
	}
	g := s.gameplay
	key := mapKey{channel: channel, mapID: c.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	var member *gameplayMember
	if room != nil {
		member = room.members[c.ID]
	}
	if member == nil || member.live != live || member.sessionID != session.SessionID || member.generation != session.Generation || !sameDropSink(member.sink, sink) {
		g.mu.Unlock()
		gate()
		live.persistMu.Unlock()
		return domain.ErrStaleOwner
	}
	hash := sha256.Sum256(wire)
	old := member.barrelRelease
	if old != nil && !old.finished && !old.executing && g.pendingCombat[old.combat.request.OperationID] != old.combat {
		// The existing resolver proved rollback and retired the pending entry.
		old.finished = true
	}
	if old != nil && (q.Key <= old.key || !old.finished) {
		// Keep a constant-size high-water mark, not an unbounded replay map.
		// A rejected or changed-body reuse cannot become another MP debit.
		g.mu.Unlock()
		gate()
		live.persistMu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	attackKey := live.nextAttackKey(now)
	a.Tick = attackKey
	id := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%d/%s/%d/%x", g.bootID, c.ID, session.Generation, session.SessionID, q.Key, hash)))
	t := &combatTicket{actor: live, sink: sink, actorSession: session, key: key, expectedActorEntry: member,
		request: domain.CombatAttackRequest{Session: session, OperationID: fmt.Sprintf("barrel-release-%x", id), ClientTick: attackKey, Family: domain.CombatSkill, SkillID: q.SkillID, LinesPerTarget: 1,
			Cost: &domain.CombatCost{Opcode: a.Opcode, MP: plan.Cost.MP, HP: plan.Cost.HP, Level: level, CooldownMS: int(plan.Cooldown.Milliseconds())}}}
	r := &barrelReleaseTicket{key: q.Key, wireHash: hash, entry: member, visit: v, combat: t, executing: true}
	t.generic = &genericAttack{attack: a, level: level, spec: spec, then: func() { s.completeBarrelRelease(r) }}
	member.barrelRelease = r
	g.mu.Unlock()
	gate()
	live.persistMu.Unlock()
	s.endSkillPrepare(channel, live, q.SkillID)
	err = s.executeCombatTicket(ctx, repo, t)
	g.mu.Lock()
	r.executing = false
	if !r.paid && g.pendingCombat[t.request.OperationID] != t {
		r.finished = true // Proven rollback/rejection; never re-execute this key.
	}
	g.mu.Unlock()
	return err
}

// Existing settlement recovery invokes the same callback after same-operation
// outcome lookup. A different visit cannot acquire this paid projectile.
func (s *Server) completeBarrelRelease(r *barrelReleaseTicket) {
	gate, err := s.lockScenePublication(r.combat.actorSession.CharacterID)
	if err != nil {
		return
	}
	defer gate()
	if !s.sceneCurrent(r.visit) {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[r.combat.key]
	if room != nil && room.members[r.combat.actorSession.CharacterID] == r.entry && r.entry.barrelRelease == r {
		r.paid, r.finished = true, true
	}
}
