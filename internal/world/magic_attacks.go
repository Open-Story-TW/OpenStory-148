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

// magicGameplay opens only the native-observed1000 family. Admission uses its
// native sweep against a static visual body envelope at the current server
// position. It does not reproduce frame selection or native previous positions;
// full range equivalence still requires the client boundary acceptance audit.
func (s *Server) magicGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, a protocol.MagicAttack) error {
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
	if c.HP <= 0 || a.SkillID != 1000 || a.TByte&15 != 1 || int(a.TByte>>4) != len(a.Targets) || len(a.Targets) > 1 || !within(c.X, c.Y, int(a.X), int(a.Y), 64) {
		return reject()
	}
	now := time.Now()
	if _, err := content.PreparePotentialPhysicalMaximum(os.Getenv("MAPLEGO_WZ_ROOT"), c, now); err != nil {
		return reject()
	}
	level := 0
	for _, state := range c.Skills {
		if state.ID == 1000 && (state.ExpiresAt.IsZero() || state.ExpiresAt.After(now)) {
			level = state.Level
		}
	}
	template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), 1000)
	if err != nil || template.Disabled || !template.CanLearnAdventurer(c.Job) || level < 1 {
		return reject()
	}
	values, err := template.Values(level)
	if err != nil || values["fixdamage"] < 1 || values["mpCon"] < 0 {
		return reject()
	}
	resources, resourceErr := skills.ResolveResourceCost(c, a.SkillID, values["mpCon"], 0, now)
	if resourceErr != nil || c.MP < resources.MP {
		return reject()
	}
	g := s.gameplay
	key := mapKey{channel: channel, mapID: c.MapID}
	objectIDs := make([]int32, 0, len(a.Targets))
	for _, target := range a.Targets {
		objectIDs = append(objectIDs, target.ObjectID)
	}
	bodies := g.snailBodies(key, objectIDs)
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live || !sameDropSink(room.members[c.ID].sink, sink) || room.members[c.ID].sessionID != session.SessionID || room.members[c.ID].generation != session.Generation {
		g.mu.Unlock()
		return reject()
	}
	m := room.members[c.ID]
	if m.attacked && (m.lastTick == a.Tick || now.Sub(m.lastAttack) < 200*time.Millisecond) {
		g.mu.Unlock()
		return reject()
	}
	ticket := &combatTicket{actor: live, sink: sink, actorSession: session, key: key, magicLevel: level, request: domain.CombatAttackRequest{Session: session, OperationID: fmt.Sprintf("combat-%s-%d-%d-%d", session.SessionID, session.Generation, a.Tick, domain.CombatThreeSnails), ClientTick: a.Tick, Family: domain.CombatThreeSnails, SkillID: 1000, LinesPerTarget: 1, Wire: append([]byte(nil), a.Wire...)}}
	for i, target := range a.Targets {
		mob := room.mobs[target.ObjectID]
		body, bodyKnown := bodies[target.ObjectID]
		if mob == nil || !bodyKnown || body.mob != mob || mob.hp <= 0 || mob.claiming || len(target.Lines) != 1 || target.Lines[0].Raw&0x80000000 != 0 || int64(target.Lines[0].Raw) > int64(values["fixdamage"]) || mob.stats.Invincible || mob.stats.OnlyNormalAttack || mob.stats.HasFixedDamage || mob.stats.FixedDamage > 0 {
			g.mu.Unlock()
			return reject()
		}
		left := a.Display&0x8000 != 0
		// Final wire coordinates are native feet at attack time and were already
		// checked against the authoritative character position above. The sweep
		// helper applies the original feetY-28 offset, never extra range padding.
		admitted, geometryErr := snailGeometryAdmits(snailPosition{int(a.X), int(a.Y)}, left, body.bounds, snailPosition{mob.life.X, mob.life.Y}, nil)
		if geometryErr != nil || !admitted {
			g.mu.Unlock()
			return reject()
		}
		damage := min(int(target.Lines[0].Raw), int(mob.lethalHP()))
		a.Targets[i].Lines[0].Damage = damage
		ticket.targets = append(ticket.targets, mob)
		ticket.request.Targets = append(ticket.request.Targets, domain.CombatTarget{ObjectID: mob.objectID, MobID: mob.stats.ID, SpawnOperationID: fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, channel, c.MapID, mob.objectID), X: mob.life.X, Y: mob.life.Y, BeforeHP: mob.hp, Damage: damage, Defeated: int64(damage) == mob.hp, Contributions: addCombatDamage(mob.contributions, session, damage), Lines: []domain.CombatDamageLine{{Raw: target.Lines[0].Raw, Applied: damage}}})
	}
	m.attacked, m.lastTick, m.lastAttack = true, a.Tick, now
	ticket.magic = &a
	for _, mob := range ticket.targets {
		mob.claiming = true
		mob.reservation = ticket
	}
	g.mu.Unlock()
	live.persistMu.Unlock()
	locked = false
	return s.executeCombatTicket(ctx, repo, ticket)
}
