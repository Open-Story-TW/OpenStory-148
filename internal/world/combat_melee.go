package world

import (
	"context"
	"fmt"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// settledMeleeGameplay is shared by the live ordinary and physical-skill
// families. The Three Snails repository uses the same settlement engine;
// its native magic input/foreign serializer remain a separate pending bridge.
func (s *Server) settledMeleeGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, a protocol.MeleeAttack, family domain.CombatFamily) error {
	repo, ok := s.repo.(domain.CombatSettlementRepository)
	if !ok || s.gameplay == nil || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
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
	session.Lease = s.currentLease()
	c := session.Character
	reject := func() error { return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP)) }
	if c.HP <= 0 || c.Job != 0 && c.Job != 100 && c.Job != 130 {
		return reject()
	}
	maxTargets, rangeLimit, cost := 1, 220, 0
	var profile content.PhysicalAttackProfile
	var normalMaximum func(content.Mob) (int, error)
	var err error
	now := time.Now()
	if family == domain.CombatPhysicalSkill {
		profile, err = content.PhysicalAttackProfileFor(os.Getenv("MAPLEGO_WZ_ROOT"), c, a.Skill, now)
		if err != nil {
			return reject()
		}
		maxTargets, rangeLimit, cost = profile.MaxTargets, profile.Range, profile.MP
		resources, resourceErr := skills.ResolveResourceCost(c, a.Skill, cost, 0, now)
		if resourceErr != nil {
			return reject()
		}
		cost = resources.MP
	} else if family != domain.CombatNormal || a.Skill != 0 {
		return reject()
	} else {
		normalMaximum, err = content.PrepareNormalAttackMaximum(os.Getenv("MAPLEGO_WZ_ROOT"), c, now)
		if err != nil {
			return reject()
		}
	}
	if len(a.Targets) > maxTargets || c.MP < cost {
		return reject()
	}
	g := s.gameplay
	key := mapKey{channel: channel, mapID: c.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live || !sameDropSink(room.members[c.ID].sink, sink) || room.members[c.ID].sessionID != session.SessionID || room.members[c.ID].generation != session.Generation {
		g.mu.Unlock()
		return reject()
	}
	member := room.members[c.ID]
	if member.attacked && now.Sub(member.lastAttack) < 200*time.Millisecond {
		g.mu.Unlock()
		return reject()
	}
	attackKey := live.nextAttackKey(now)
	ticket := &combatTicket{actor: live, sink: sink, actorSession: session, key: key, attack: a, request: domain.CombatAttackRequest{Session: session, OperationID: fmt.Sprintf("combat-%s-%d-%d-%d", session.SessionID, session.Generation, attackKey, family), ClientTick: attackKey, Family: family, SkillID: a.Skill}}
	seen := map[int32]bool{}
	killed := false
	for i, target := range a.Targets {
		mob := room.mobs[target.ObjectID]
		if seen[target.ObjectID] || mob == nil || mob.hp <= 0 || mob.claiming || !within(c.X, c.Y, mob.life.X, mob.life.Y, rangeLimit) || target.Damage < 0 {
			g.mu.Unlock()
			return reject()
		}
		maximum := 0
		if family == domain.CombatNormal {
			maximum, err = normalMaximum(mob.stats)
		} else {
			maximum, err = profile.MaximumDamage(mob.stats)
		}
		if err != nil {
			g.mu.Unlock()
			return reject()
		}
		damage := min(target.Damage, maximum, int(mob.lethalHP()))
		a.Targets[i].Damage = damage
		q := domain.CombatTarget{ObjectID: mob.objectID, MobID: mob.stats.ID, SpawnOperationID: fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, channel, c.MapID, mob.objectID), X: mob.life.X, Y: mob.life.Y, BeforeHP: mob.hp, Damage: damage, Defeated: int64(damage) == mob.hp, Contributions: addCombatDamage(mob.contributions, session, damage),
			Lines: []domain.CombatDamageLine{{Raw: uint32(target.Damage), Applied: damage}}}
		ticket.request.Targets = append(ticket.request.Targets, q)
		ticket.targets = append(ticket.targets, mob)
		seen[target.ObjectID] = true
		killed = killed || q.Defeated
	}
	member.attacked, member.lastTick, member.lastAttack = true, a.Tick, now
	ticket.attack = a
	if family == domain.CombatNormal && !killed {
		// No durable cost or reward exists for an ordinary nonlethal swing. Its
		// effective damage and contribution are one room-serialized memory change.
		for i, mob := range ticket.targets {
			q := ticket.request.Targets[i]
			mob.hp -= int64(q.Damage)
			ticket.recordAppliedDamageLocked(q, mob.hp, false)
			mob.contributions = q.Contributions
		}
		ticket.observers = combatRoomPeers(room)
		g.mu.Unlock()
		live.persistMu.Unlock()
		locked = false
		s.publishCombatTicket(ctx, ticket, domain.CombatAttackResult{})
		return nil
	}
	for _, mob := range ticket.targets {
		mob.claiming = true
		mob.reservation = ticket
	}
	g.mu.Unlock()
	// Reacquire all required live persistMu in ascending character order; never
	// enter the multi-recipient transaction while holding the actor out of order.
	live.persistMu.Unlock()
	locked = false
	return s.executeCombatTicket(ctx, repo, ticket)
}
