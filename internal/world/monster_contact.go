package world

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func mindControlOwner(mob *gameplayMob, session domain.CharacterSession, now time.Time) (*combat.ActiveStatus, bool) {
	s := mob.statuses[combat.MobDazzle]
	if s == nil || !s.ExpiresAt.After(now) {
		return nil, false
	}
	if s.OwnerID == session.CharacterID && s.Value == int32(session.CharacterID) &&
		s.OwnerSession == session.SessionID && s.OwnerGeneration == session.Generation && combat.IsMindControlSkill(int(s.SkillID)) {
		return s, true
	}
	return nil, true
}

func mobContactWithin(source, target *gameplayMob, a, b content.MobBodyRect, x, y int) bool {
	if !a.Nonempty() || !b.Nonempty() {
		return false
	}
	sx, sy := source.life.X, source.life.Y
	tx, ty := target.life.X, target.life.Y
	return x >= tx+b.Left && x < tx+b.Right && y >= ty+b.Top && y < ty+b.Bottom &&
		sx+a.Left < tx+b.Right && tx+b.Left < sx+a.Right && sy+a.Top < ty+b.Bottom && ty+b.Top < sy+a.Bottom
}

func (s *Server) monsterContactGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, wire []byte) error {
	m, err := protocol.ReadMonsterContact(protocol.NewReader(wire))
	repo, ok := s.repo.(domain.CombatSettlementRepository)
	if err != nil || !ok || s.gameplay == nil {
		return nil
	}
	expected := s.sceneFor(live, sink)
	if expected == nil || expected.owner.ChannelID != channel || expected.owner.CharacterID != int64(m.Character) {
		return nil
	}
	live.persistMu.Lock()
	locked := true
	defer func() {
		if locked {
			live.persistMu.Unlock()
		}
	}()
	live.mu.Lock()
	session := cloneCombatSession(live.session)
	live.mu.Unlock()
	releaseScene, err := s.lockScenePublication(session.CharacterID)
	if err != nil {
		return err
	}
	defer func() {
		if locked {
			releaseScene()
		}
	}()
	if session.Character.HP <= 0 || !s.sceneCurrent(expected) {
		return nil
	}
	session.Lease = s.currentLease()
	key := mapKey{channel: channel, mapID: session.Character.MapID}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil {
		g.mu.Unlock()
		return nil
	}
	member := room.members[session.CharacterID]
	source, target := room.mobs[m.Source], room.mobs[m.Target]
	now := time.Now()
	if member == nil || member.visit != expected || member.sessionID != session.SessionID || member.generation != session.Generation ||
		source == nil || target == nil || source.hp <= 0 || target.hp <= 0 || source.claiming || target.claiming ||
		target.stats.Invincible || target.statuses.BlocksDamage(false) ||
		now.Add(50*time.Millisecond).Before(target.nextMobContact) {
		g.mu.Unlock()
		return nil
	}
	from, fromDazzled := mindControlOwner(source, session, now)
	to, toDazzled := mindControlOwner(target, session, now)
	// The native Dazzle owner reports contact even if another client drives
	// both monsters' movement (v148 0x169). Authenticate against the effect.
	if fromDazzled == toDazzled || from == nil && to == nil {
		g.mu.Unlock()
		return nil
	}
	control := from
	if control == nil {
		control = to
	}
	skill := int(control.SkillID)
	var skillLevel int
	for _, state := range session.Character.Skills {
		if state.ID == skill && (state.ExpiresAt.IsZero() || state.ExpiresAt.After(now)) {
			skillLevel = state.Level
			break
		}
	}
	if skillLevel < 1 {
		g.mu.Unlock()
		return nil
	}
	a, errA := g.catalog.MobBodyBounds(source.stats.ID)
	b, errB := g.catalog.MobBodyBounds(target.stats.ID)
	if errA != nil || errB != nil || !mobContactWithin(source, target, a.Envelope, b.Envelope, m.X, m.Y) {
		g.mu.Unlock()
		return nil
	}
	pad := source.stats.PAD
	if bonus := source.statuses[combat.MobPAD]; bonus != nil && bonus.ExpiresAt.After(now) {
		pad += int(bonus.Value)
	}
	minDamage, maxDamage := combat.MonsterContactBounds(pad, target.statuses.DefenseRate(target.stats.PDRate, false))
	if int(m.Damage) < minDamage || int(m.Damage) > maxDamage {
		g.mu.Unlock()
		return nil
	}
	damage := int(min(int64(m.Damage), target.lethalHP()))
	if damage <= 0 {
		g.mu.Unlock()
		return nil
	}
	// v148 normally enforces 2000ms on the victim. Keep a packet-transport
	// allowance while advancing the full interval even when the report is early.
	anchor := target.nextMobContact
	if now.After(anchor) {
		anchor = now
	}
	target.nextMobContact = anchor.Add(2 * time.Second)
	attackKey := live.nextAttackKey(now)
	q := domain.CombatTarget{ObjectID: target.objectID, MobID: target.stats.ID,
		SpawnOperationID: fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, channel, key.mapID, target.objectID),
		X:                target.life.X, Y: target.life.Y, BeforeHP: target.hp, Damage: damage, Defeated: int64(damage) == target.hp,
		Contributions: addCombatDamage(target.contributions, session, damage),
		Lines:         []domain.CombatDamageLine{{Raw: uint32(m.Damage), Applied: damage}}}
	ticket := &combatTicket{actor: live, sink: sink, actorSession: session, key: key, expectedActorEntry: member,
		monsterContact: protocol.MonsterContactDamage(target.objectID, source.stats.ID, m.Damage, m.Facing),
		targets:        []*gameplayMob{target}, request: domain.CombatAttackRequest{Session: session,
			OperationID: fmt.Sprintf("combat-%s-%d-%d-%d", session.SessionID, session.Generation, attackKey, domain.CombatSkill),
			ClientTick:  attackKey, Family: domain.CombatSkill, SkillID: skill, LinesPerTarget: 1,
			Cost: &domain.CombatCost{Opcode: protocol.RecvMonsterContact, Level: skillLevel}, Wire: append([]byte(nil), wire...),
			Targets: []domain.CombatTarget{q}}}
	if q.Defeated {
		target.claiming, target.reservation = true, ticket
	} else {
		target.hp -= int64(damage)
		target.contributions = q.Contributions
		ticket.recordAppliedDamageLocked(q, target.hp, false)
		ticket.observers = combatRoomPeers(room)
	}
	g.mu.Unlock()
	releaseScene()
	live.persistMu.Unlock()
	locked = false
	if q.Defeated {
		return s.executeCombatTicket(ctx, repo, ticket)
	}
	if traceCharacterEnabled(session.CharacterID) {
		slog.Info("monster_contact_applied", "character", session.CharacterID, "skill", skill, "source", m.Source,
			"target", m.Target, "before_hp", q.BeforeHP, "after_hp", q.BeforeHP-int64(damage), "original_damage", m.Damage,
			"applied", damage, "minimum", minDamage, "maximum", maxDamage)
	}
	s.publishCombatTicket(ctx, ticket, domain.CombatAttackResult{})
	return nil
}
