package world

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"sync/atomic"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

// roomSummon is a transient summon owned by one field visit. It disappears
// on expiry, owner leave (map change, channel change, logout), explicit
// removal, puppet death, or after one attack for single-attack summons.
type roomSummon struct {
	objectID             int32
	ownerID              int64
	ownerSession         string
	ownerGeneration      int64
	ownerEntry           *gameplayMember // Exact field visit; never reused after leave/reentry.
	ownerLevel           int
	look                 *domain.Character // Owner avatar for Mirrored Target.
	skillID, level       int
	source               int // Cast skill: spec, level and cost authority (skillID may be a variant).
	x, y                 int
	foothold             int
	spec                 *combat.SummonSpec
	expiresAt            time.Time
	deathEffect          combat.Effect
	deathWindow          time.Duration
	amplifier            combat.Effect
	amplifierAreaActive  bool
	hp                   int
	lastTick             uint32
	lastAttack, lastHeal time.Time
	attacked             bool
	// teslaState and teslaPoints are Tesla Coil's triangle (protocol.SummonView).
	teslaState  byte
	teslaPoints [3][2]int16
}

func (m *roomSummon) attackInLifetime(now time.Time, action byte) bool {
	if m.spec.ExpiryAction == 0 {
		return now.Before(m.expiresAt)
	}
	// The native animation clock and the world expiry are sampled on separate
	// loops. Accept a small frame lead and retain the exact object until the
	// WZ death animation interval ends, without accepting another action.
	return action == m.spec.ExpiryAction && !m.attacked &&
		!now.Before(m.expiresAt.Add(-150*time.Millisecond)) && now.Before(m.expiresAt.Add(m.deathWindow))
}

func (m *roomSummon) removalTime() time.Time {
	if m.spec.ExpiryAction != 0 && !m.attacked {
		return m.expiresAt.Add(m.deathWindow)
	}
	return m.expiresAt
}

func (m *roomSummon) view(animated bool) protocol.SummonView {
	stance := m.spec.Stance
	if stance == 0 {
		stance = 4
	}
	return protocol.SummonView{OwnerID: m.ownerID, ObjectID: m.objectID, SkillID: m.skillID, OwnerLevel: m.ownerLevel, SkillLevel: m.level, X: int16(m.x), Y: int16(m.y), Stance: stance, Foothold: int16(m.foothold), MoveAbility: m.spec.MoveAbility, AssistType: m.spec.AssistType, Animated: animated, Look: m.look,
		TeslaState: m.teslaState, TeslaPoints: m.teslaPoints}
}

const af11Skill = 35121010

func (m *roomSummon) amplifierAreaPacket(now time.Time) []byte {
	if !m.amplifierAreaActive || !now.Before(m.expiresAt) {
		return nil
	}
	lt, rb := m.amplifier.Box.At(combat.Point{X: m.x, Y: m.y}, false)
	// Original v148 CalcDamage looks up AF-11 by source in CAffectedAreaPool
	// (0x42D47D), separate from the visible CSummoned robot (0x1A1).
	// The native type-0 lookup (0x42D276) applies field damage to everyone.
	return protocol.AffectedAreaCreated(protocol.AffectedArea{ObjectID: m.objectID, Type: protocol.AreaPlayer, OwnerID: m.ownerID,
		SkillID: m.skillID, SkillLevel: m.level, Box: protocol.Rect{Left: int32(lt.X), Top: int32(lt.Y), Right: int32(rb.X), Bottom: int32(rb.Y)},
		X: int16(m.x), Y: int16(m.y)})
}

func (m *roomSummon) removeAmplifierArea() []byte {
	if !m.amplifierAreaActive {
		return nil
	}
	m.amplifierAreaActive = false
	return protocol.AffectedAreaRemoved(m.objectID, m.skillID, false)
}

func (m *roomSummon) removePackets(room *gameplayRoom, reason byte) []roomDelivery {
	out := roomBroadcast(room, m.removeAmplifierArea(), -1)
	return append(out, roomBroadcast(room, protocol.RemoveSummon(m.ownerID, m.objectID, reason), -1)...)
}

func removeReason(skillID int) byte {
	switch {
	case skillID == 35121003:
		return protocol.SummonLeaveSatellite
	case skillID/1000000 == 35 || skillID == 33101008:
		return protocol.SummonLeaveMech
	}
	return protocol.SummonLeaveAnimated
}

type roomDelivery struct {
	sink   packetSink
	packet []byte
}

func roomBroadcast(room *gameplayRoom, p []byte, except int64) []roomDelivery {
	if p == nil {
		return nil
	}
	var out []roomDelivery
	for id, m := range room.members {
		if id != except {
			out = append(out, roomDelivery{m.sink, p})
		}
	}
	return out
}

func deliver(ds []roomDelivery) {
	for _, d := range ds {
		_ = d.sink.WritePacket(d.packet)
	}
}

// SpawnSummon creates the summon for a successful cast. S0a's cast path calls
// it when attackSpecFor(skill).Summon is set; the summon buff itself is
// S0a's. Previous same-skill summons are replaced unless the spec is Multi.
func (s *Server) SpawnSummon(channel int, live *liveCharacter, skillID, level int, now time.Time) (int32, error) {
	return s.spawnSummonAt(channel, live, skillID, level, nil, now, time.Time{})
}

// spawnSummonAt spawns at pos (a parent summon's position) or the owner's.
// A non-zero until shortens the summon's life to end then (a summon restored
// with the time its buff has left).
func (s *Server) spawnSummonAt(channel int, live *liveCharacter, skillID, level int, pos *combat.Point, now, until time.Time) (int32, error) {
	spec, ok := attackSpecFor(skillID)
	if !ok || spec.Summon == nil || level < 1 || s.gameplay == nil {
		return 0, fmt.Errorf("skill%d has no registered summon", skillID)
	}
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	d := spec.Summon.Duration
	hp := spec.Summon.HP
	var deathEffect combat.Effect
	var deathWindow time.Duration
	var amplifier combat.Effect
	if d == 0 || spec.Summon.Puppet && hp == 0 || spec.Summon.ExpiryAction != 0 {
		if template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), skillID); err == nil {
			if spec.Summon.ExpiryAction != 0 {
				deathEffect, deathWindow, err = combat.SummonExpiryEffect(template, level)
				if err != nil {
					return 0, err
				}
			}
			if key := spec.Summon.DurationKey; d == 0 && key != "" {
				if values, err := template.Values(min(level, template.MaxLevel)); err == nil {
					d = time.Duration(values[key]) * time.Second
				}
			}
			if e, err := combat.FromSkill(template, level); err == nil {
				if skillID == af11Skill {
					amplifier = e
				}
				if d == 0 {
					d = e.Time
				}
				if hp == 0 {
					hp = e.X // Java MapleSummon puppet HP is the level's x.
				}
			}
		}
	}
	if d <= 0 {
		return 0, fmt.Errorf("skill%d summon has no duration", skillID)
	}
	if spec.Summon.ExpiryAction != 0 && deathWindow == 0 {
		return 0, fmt.Errorf("skill%d summon has no death attack data", skillID)
	}
	if skillID == af11Skill && (!amplifier.Box.Set || amplifier.X <= 0 || amplifier.Y <= 0) {
		return 0, fmt.Errorf("skill%d lacks the v148 amplification area or percentages", skillID)
	}
	if !until.IsZero() {
		if !until.After(now) {
			return 0, fmt.Errorf("skill%d summon already expired", skillID)
		}
		d = min(d, until.Sub(now))
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	if room == nil || room.members[c.ID] == nil || room.members[c.ID].live != live {
		g.mu.Unlock()
		return 0, fmt.Errorf("summon owner not in field")
	}
	var out []roomDelivery
	if !spec.Summon.Multi {
		for id, m := range room.summons {
			if m.ownerID == c.ID && m.source == skillID {
				delete(room.summons, id)
				out = append(out, m.removePackets(room, protocol.SummonLeaveNone)...)
			}
		}
	}
	oid := g.alloc()
	if oid == 0 {
		g.mu.Unlock()
		return 0, fmt.Errorf("object ID space exhausted")
	}
	m := &roomSummon{objectID: oid, ownerID: c.ID, ownerSession: session.SessionID, ownerGeneration: session.Generation, ownerEntry: room.members[c.ID], ownerLevel: c.Level, skillID: skillID, source: skillID, level: level, x: c.X, y: c.Y, spec: spec.Summon, expiresAt: now.Add(d), deathEffect: deathEffect, deathWindow: deathWindow, hp: hp, lastHeal: now}
	if skillID == af11Skill {
		m.amplifier, m.amplifierAreaActive = amplifier, true
	}
	if pos != nil {
		m.x, m.y = pos.X, pos.Y
	}
	if skillID == 4341006 {
		look := c
		m.look = &look
	}
	if v := spec.Summon.Variants; len(v) > 0 {
		m.skillID = v[rand.IntN(len(v))] // Elemental Knights 23111008: one of 23111008/9/10.
	}
	if room.summons == nil {
		room.summons = map[int32]*roomSummon{}
	}
	room.summons[oid] = m
	if skillID == teslaCoil {
		out = append(out, formTeslaTriangleLocked(room, m)...)
	}
	spawn := protocol.SpawnSummon(m.view(true))
	summonDiag("summon_spawned", c.ID, spawn)
	out = append(out, roomBroadcast(room, spawn, -1)...)
	out = append(out, roomBroadcast(room, m.amplifierAreaPacket(now), -1)...)
	for _, twin := range spec.Summon.Twins {
		tid := g.alloc()
		if tid == 0 {
			break
		}
		t := *m
		t.objectID, t.skillID = tid, twin
		room.summons[tid] = &t
		spawn = protocol.SpawnSummon(t.view(true))
		summonDiag("summon_spawned", c.ID, spawn)
		out = append(out, roomBroadcast(room, spawn, -1)...)
	}
	g.mu.Unlock()
	deliver(out)
	return oid, nil
}

// RemoveSummons drops an owner's summons of one skill (0 = all), e.g. when
// S0a cancels the summon buff.
func (s *Server) RemoveSummons(channel, mapID int, ownerID int64, skillID int, expected ...domain.CharacterSession) {
	if s.gameplay == nil {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: mapID}]
	var out []roomDelivery
	if room != nil {
		out = removeOwnerSummonsLocked(room, ownerID, skillID, expected...)
	}
	g.mu.Unlock()
	deliver(out)
}

func removeOwnerSummonsLocked(room *gameplayRoom, ownerID int64, skillID int, expected ...domain.CharacterSession) []roomDelivery {
	var out []roomDelivery
	for id, m := range room.summons {
		if m.ownerID == ownerID && (skillID == 0 || m.source == skillID) {
			if len(expected) > 0 && (m.ownerSession != expected[0].SessionID || m.ownerGeneration != expected[0].Generation) {
				continue
			}
			delete(room.summons, id)
			// The local CSummoned survives a buff reset unless its owner also
			// receives 0x183. Java broadcasts this removal to the whole field.
			out = append(out, m.removePackets(room, removeReason(m.skillID))...)
		}
	}
	return out
}

// summonEnterPackets lists existing summons for a late joiner.
func summonEnterPacketsLocked(room *gameplayRoom) [][]byte {
	var out [][]byte
	now := time.Now()
	for _, m := range room.summons {
		out = append(out, protocol.SpawnSummon(m.view(false)))
		if area := m.amplifierAreaPacket(now); area != nil {
			out = append(out, area)
		}
	}
	return out
}

// tickSummons expires summons and runs periodic party heals.
func (g *gameplaySpace) tickSummons(now time.Time) {
	var out []roomDelivery
	g.mu.Lock()
	for _, room := range g.rooms {
		for id, m := range room.summons {
			if m.amplifierAreaActive && !now.Before(m.expiresAt) {
				out = append(out, roomBroadcast(room, m.removeAmplifierArea(), -1)...)
			}
			if !now.Before(m.removalTime()) {
				delete(room.summons, id)
				out = append(out, m.removePackets(room, removeReason(m.skillID))...)
				out = append(out, breakTeslaTriangleLocked(room, m)...)
			}
		}
	}
	g.mu.Unlock()
	deliver(out)
}

func (s *Server) ownSummonLocked(room *gameplayRoom, live *liveCharacter, oid int32) *roomSummon {
	m := room.summons[oid]
	if m == nil {
		return nil
	}
	member := room.members[m.ownerID]
	if member == nil || member != m.ownerEntry || member.live != live || member.sessionID != m.ownerSession || member.generation != m.ownerGeneration {
		return nil
	}
	return m
}

func hasActiveSummon(room *gameplayRoom, owner *gameplayMember, skillID int, now time.Time) bool {
	for _, m := range room.summons {
		if m.ownerEntry == owner && m.source == skillID && m.skillID == skillID && m.expiresAt.After(now) {
			return true
		}
	}
	return false
}

// amplifierDamageLocked matches the original affected-area membership check.
// Overlapping amplifiers do not stack. The field pool tries the newest first.
func amplifierDamageLocked(room *gameplayRoom, c domain.Character, party domain.PartySnapshot, now time.Time) (physical, magic int) {
	var selected int32
	for _, m := range room.summons {
		if !m.amplifierAreaActive || m.objectID <= selected || !now.Before(m.expiresAt) || room.members[m.ownerID] != m.ownerEntry {
			continue
		}
		if c.ID != m.ownerID {
			if _, same := party.Member(m.ownerID); party.ID == 0 || !same {
				continue
			}
		}
		lt, rb := m.amplifier.Box.At(combat.Point{X: m.x, Y: m.y}, false)
		// The v148 receiver's PtInRect treats right and bottom as exclusive.
		if c.X >= lt.X && c.X < rb.X && c.Y >= lt.Y && c.Y < rb.Y {
			selected, physical, magic = m.objectID, m.amplifier.X, m.amplifier.Y
		}
	}
	return physical, magic
}

func (s *Server) summonMove(channel int, live *liveCharacter, mv protocol.SummonMoveInput) {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	var out []roomDelivery
	if room != nil {
		if m := s.ownSummonLocked(room, live, mv.ObjectID); m != nil && m.spec.MoveAbility != 0 {
			if mv.Changed {
				m.x, m.y = mv.X, mv.Y
			}
			out = roomBroadcast(room, protocol.MoveSummon(m.ownerID, m.objectID, mv.StartX, mv.StartY, mv.Fragments), m.ownerID)
		}
	}
	g.mu.Unlock()
	deliver(out)
}

// cancelSummon handles native0x103: the object ID names an owned summon, not
// a skill ID. Java cancels its SUMMON buff too (except Healing Robot35121011).
// Pin ownership under persistMu before SQL so a stale object cannot initiate
// cancellation of a replacement cast. Pending settlements keep both buff and object unchanged.
func (s *Server) cancelSummon(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) error {
	if s.gameplay == nil {
		return nil
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.skillSession(live)
	if session.ChannelID != channel || s.skillMemberFor(session, live, sink) == nil || s.sceneFor(live, sink) == nil || s.combatVitalsPending(live, session) {
		return sink.WritePacket(protocol.EnableActions())
	}
	if err := assetOutcomeUncertain(live, session); err != nil {
		return err
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: session.Character.MapID}]
	if room == nil {
		g.mu.Unlock()
		return nil
	}
	m := s.ownSummonLocked(room, live, oid)
	if m == nil || m.ownerID != session.CharacterID || !sameDropSink(room.members[session.CharacterID].sink, sink) || !m.expiresAt.After(time.Now()) || m.skillID == 35111002 || m.skillID == 35121010 {
		g.mu.Unlock()
		return nil
	}
	if m.skillID == 35121011 {
		delete(room.summons, oid)
		out := roomBroadcast(room, protocol.RemoveSummon(m.ownerID, oid, removeReason(m.skillID)), -1)
		g.mu.Unlock()
		deliver(out)
		return nil
	}
	id := m.source
	g.mu.Unlock()
	return s.cancelSkillLocked(ctx, live, sink, id)
}

func (s *Server) summonDamaged(channel int, live *liveCharacter, d protocol.SummonDamageInput) {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	var out []roomDelivery
	if room != nil {
		if m := s.ownSummonLocked(room, live, d.ObjectID); m != nil && m.spec.Puppet && d.Damage > 0 {
			m.hp -= d.Damage
			out = roomBroadcast(room, protocol.DamageSummonPacket(m.ownerID, m.objectID, d.AttackIndex, d.Damage, d.MobTemplate, false), m.ownerID)
			if m.hp <= 0 {
				delete(room.summons, m.objectID)
				out = append(out, roomBroadcast(room, protocol.RemoveSummon(m.ownerID, m.objectID, removeReason(m.skillID)), -1)...)
			}
		}
	}
	g.mu.Unlock()
	deliver(out)
}

// summonAttackGameplay validates a summon's attack against its registered
// spec at the summon's server position and settles like any skill attack
// (no cost; kills reward the owner's party).
func (s *Server) summonAttackGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, in protocol.SummonAttackInput) error {
	repo, ok := s.repo.(domain.CombatSettlementRepository)
	if !ok || s.gameplay == nil {
		return nil
	}
	lineCount := 1
	if len(in.Targets) > 0 {
		lineCount = len(in.Targets[0].Lines)
	}
	if lineCount < 1 || len(in.Targets)*lineCount > 15 {
		return nil
	}
	for _, target := range in.Targets {
		if len(target.Lines) != lineCount {
			return nil
		}
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
	now := time.Now()
	g := s.gameplay
	key := mapKey{channel: channel, mapID: c.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || c.HP <= 0 {
		g.mu.Unlock()
		return nil
	}
	m := s.ownSummonLocked(room, live, in.ObjectID)
	if m == nil {
		g.mu.Unlock()
		slog.Warn("summon_attack_rejected", "character", c.ID, "summon", in.ObjectID, "reason", "summon_not_owned")
		return nil
	}
	ownerEntry := room.members[c.ID]
	spec, registered := attackSpecFor(m.source)
	if !registered || spec.Summon == nil || m.spec != spec.Summon || !spec.Allows(combat.Summoned) ||
		!m.attackInLifetime(now, in.Action) || m.attacked && (m.lastTick == in.Tick || now.Sub(m.lastAttack) < 200*time.Millisecond) || attackSkillLevel(c, levelSource(m.source, spec), now) < 1 {
		g.mu.Unlock()
		slog.Warn("summon_attack_guard_rejected", "character", c.ID, "skill", m.source, "registered", registered, "kind", spec.Kind, "learned", attackSkillLevel(c, levelSource(m.source, spec), now), "summon_level", m.level)
		return nil
	}
	effect := spec.Effect
	if m.spec.ExpiryAction != 0 {
		effect = m.deathEffect
	}
	if effect.SkillID == 0 {
		g.mu.Unlock()
		template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), m.source)
		if err != nil {
			return nil
		}
		if effect, err = combat.SummonEffect(template, m.level); err != nil {
			return nil
		}
		g.mu.Lock()
		if room = g.rooms[key]; room == nil || s.ownSummonLocked(room, live, in.ObjectID) != m {
			g.mu.Unlock()
			return nil
		}
	}
	if m.source == 35121003 {
		// v148 SG-88 scales the robot slam by its gauge at 0x73133B; the
		// operator's separate body attack uses the unscaled WZ effect.
		charge := int(min(in.ChargeGauge, 100))
		effect.Damage += effect.Damage * charge / 100
	}
	// Preparing checked resource/stat snapshots may read immutable WZ. Never
	// hold the global room mutex during this phase, then revalidate the exact
	// summon and owner visit before planning its already-paid hit.
	g.mu.Unlock()
	stats, err := combat.AttackStats(c, now)
	g.mu.Lock()
	if room = g.rooms[key]; room == nil || room.members[c.ID] != ownerEntry || ownerEntry.sessionID != session.SessionID || ownerEntry.generation != session.Generation || !sameDropSink(ownerEntry.sink, sink) || s.ownSummonLocked(room, live, in.ObjectID) != m || !m.attackInLifetime(time.Now(), in.Action) {
		g.mu.Unlock()
		return nil
	}
	if err != nil {
		g.mu.Unlock()
		return nil
	}
	// The summon moves client-side between 0xFF updates; accept its reported
	// attack position when it is near the server-tracked one.
	ax, ay := m.x, m.y
	if m.spec.ExpiryAction == 0 && within(m.x, m.y, int(in.X), int(in.Y), 300) {
		ax, ay = int(in.X), int(in.Y)
	}
	a := protocol.Attack{Opcode: protocol.RecvSummonAttack, SkillID: m.source, TByte: byte(len(in.Targets)<<4 | lineCount), Tick: in.Tick, X: int16(ax), Y: int16(ay), Targets: in.Targets, Wire: append([]byte(nil), in.Wire...)}
	if in.Action&0x80 != 0 {
		a.Display = 0x8000
	}
	actx := combat.AttackContext{Kind: combat.Summoned, Character: c, Stats: stats, Effect: effect, Attack: a, Now: now, SummonSkillID: m.skillID}
	var deathLT, deathRB combat.Point
	if m.spec.ExpiryAction != 0 {
		deathLT, deathRB = effect.Box.At(combat.Point{X: m.x, Y: m.y}, false)
	}
	var mobs []*gameplayMob
	for _, t := range in.Targets {
		mob := room.mobs[t.ObjectID]
		if mob == nil || mob.hp <= 0 || mob.claiming {
			g.mu.Unlock()
			slog.Warn("summon_attack_rejected", "character", c.ID, "skill", m.source, "reason", "target_missing", "target", t.ObjectID)
			return nil
		}
		if m.spec.ExpiryAction != 0 && !combat.Inside(deathLT, deathRB, combat.Point{X: mob.life.X, Y: mob.life.Y}) {
			g.mu.Unlock()
			slog.Warn("summon_attack_rejected", "character", c.ID, "skill", m.source, "reason", "expiry_target_outside", "target", t.ObjectID)
			return nil
		}
		actx.Targets = append(actx.Targets, combat.TargetInfo{ObjectID: mob.objectID, Mob: mob.stats, X: mob.life.X, Y: mob.life.Y, HP: mob.hp, Statuses: mob.statuses})
		mobs = append(mobs, mob)
	}
	plan, err := combat.PlanAttack(spec, actx, rollProp)
	if err != nil {
		g.mu.Unlock()
		slog.Warn("summon_attack_plan_rejected", "character", c.ID, "skill", m.source, "error", err)
		return nil
	}
	m.attacked, m.lastTick, m.lastAttack = true, in.Tick, now
	family := domain.CombatSkill
	gen := &genericAttack{attack: a, level: m.level, summon: m, summonHitOrder: in.HitOrder}
	ticket := &combatTicket{actor: live, sink: sink, actorSession: session, key: key, generic: gen,
		request: domain.CombatAttackRequest{Session: session, OperationID: fmt.Sprintf("summon-%s-%d-%d-%d", session.SessionID, session.Generation, m.objectID, in.Tick), ClientTick: in.Tick, Family: family, SkillID: m.source, LinesPerTarget: lineCount, Cost: &domain.CombatCost{Opcode: protocol.RecvSummonAttack, Level: m.level, Summon: m.objectID}}}
	killed := false
	for i, mob := range mobs {
		lines := append([]int(nil), plan.Targets[i].Lines...)
		damage := int(combat.ClipToHP(lines, mob.lethalHP()))
		q := domain.CombatTarget{ObjectID: mob.objectID, MobID: mob.stats.ID, SpawnOperationID: fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, channel, c.MapID, mob.objectID), X: mob.life.X, Y: mob.life.Y, BeforeHP: mob.hp, Damage: damage, Defeated: int64(damage) == mob.hp, Contributions: addCombatDamage(mob.contributions, session, damage)}
		for j, line := range in.Targets[i].Lines {
			q.Lines = append(q.Lines, domain.CombatDamageLine{Raw: line.Raw, Applied: lines[j], Long: line.Long})
			gen.attack.Targets[i].Lines[j].Damage = lines[j]
		}
		ticket.request.Targets = append(ticket.request.Targets, q)
		ticket.targets = append(ticket.targets, mob)
		gen.statuses = append(gen.statuses, plan.Targets[i].Statuses)
		killed = killed || q.Defeated
	}
	gen.summonAction = in.Action
	gen.summonOwnerLevel = c.Level
	if m.spec.SingleAttack {
		delete(room.summons, m.objectID)
		gen.extraOut = append(gen.extraOut, protocol.RemoveSummon(m.ownerID, m.objectID, removeReason(m.source)))
	}
	if !killed {
		for i, mob := range ticket.targets {
			q := ticket.request.Targets[i]
			mob.hp -= int64(q.Damage)
			ticket.recordAppliedDamageLocked(q, mob.hp, false)
			mob.contributions = q.Contributions
		}
		s.applyGenericStatusesLocked(ticket, now)
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
	live.persistMu.Unlock()
	locked = false
	return s.executeCombatTicket(ctx, repo, ticket)
}

// summonSub handles RecvSubSummon for a summon whose spec has Sub (Beholder).
func (s *Server) summonSub(channel int, live *liveCharacter, sink packetSink, in protocol.SubSummonInput) error {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	session := live.session
	live.mu.Unlock()
	c := session.Character
	now := time.Now()
	if c.HP <= 0 || s.combatVitalsPending(live, session) {
		return nil
	}
	if in.SkillID == 0 {
		// Some sub-summon packets (Healing Robot) carry only the summon oid.
		s.gameplay.mu.Lock()
		if room := s.gameplay.rooms[mapKey{channel: channel, mapID: c.MapID}]; room != nil {
			if own := s.ownSummonLocked(room, live, in.ObjectID); own != nil {
				in.SkillID = own.source
			}
		}
		s.gameplay.mu.Unlock()
		if in.SkillID == 0 {
			return nil
		}
	}
	subLevel := attackSkillLevel(c, in.SkillID, now)
	if subLevel == 0 {
		subLevel = skills.EquipmentRelatedSkillLevel(c, in.SkillID, now)
	}
	if subLevel < 1 {
		return nil
	}
	var values map[string]int
	if template, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), in.SkillID); err == nil {
		values, _ = template.Values(subLevel)
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel: channel, mapID: c.MapID}]
	var m *roomSummon
	if room != nil {
		m = s.ownSummonLocked(room, live, in.ObjectID)
	}
	if m == nil || m.spec.Sub == nil {
		g.mu.Unlock()
		return nil
	}
	res, err := m.spec.Sub(combat.SubSummonContext{Character: c, SummonSkill: m.skillID, SummonLvl: m.level, SubSkill: in.SkillID, SubLevel: subLevel, SubValues: values, LastSub: m.lastHeal, Now: now})
	if err != nil {
		g.mu.Unlock()
		return nil
	}
	m.lastHeal = now
	out := roomBroadcast(room, protocol.SummonSkillPacket(m.ownerID, m.objectID, res.Action), -1)
	g.mu.Unlock()
	if res.HealHP > 0 {
		live.mu.Lock()
		cur := &live.session.Character
		if live.session.SessionID == session.SessionID && live.session.Generation == session.Generation && cur.HP > 0 {
			cur.HP = min(cur.MaxHP, cur.HP+res.HealHP)
			live.dirty = true
		}
		hp, mp := cur.HP, cur.MP
		live.mu.Unlock()
		_ = sink.WritePacket(protocol.UpdateHPMP(hp, mp))
		s.refreshParty(live)
	}
	deliver(out)
	for _, child := range res.Spawn {
		at := combat.Point{X: m.x, Y: m.y - 5}
		if _, err := s.spawnSummonAt(channel, live, child.SkillID, child.Level, &at, now, time.Time{}); err != nil {
			slog.Warn("sub_summon_spawn_failed", "character", c.ID, "skill", child.SkillID, "error", err)
		}
	}
	if res.Apply != nil {
		res.Apply()
	}
	return nil
}

// summonDiagBudget bounds the acceptance diagnostics for summon wire traffic
// (live summon packets are not yet verified natively).
var summonDiagBudget, summonMoveDiag atomic.Int32

func summonDiag(msg string, characterID int64, packet []byte) {
	if summonDiagBudget.Add(1) > 200 || len(packet) > 256 {
		return
	}
	slog.Info(msg, "character", characterID, "hex", hex.EncodeToString(packet))
}

// summonFieldPacket handles summon and town-portal input from a detached body.
func (s *Server) summonFieldPacket(ctx context.Context, channel int, live *liveCharacter, sink packetSink, op uint16, body []byte) error {
	live.mu.Lock()
	characterID := live.session.CharacterID
	live.mu.Unlock()
	r := protocol.NewReader(body)
	switch op {
	case protocol.RecvUseTownPortal:
		u, err := protocol.ReadUseTownPortal(r)
		if err != nil {
			slog.Warn("town_portal_use_rejected", "character", characterID, "reason", "parse", "hex", hex.EncodeToString(body))
			return sink.WritePacket(protocol.EnableActions())
		}
		return s.useTownPortal(ctx, channel, live, sink, u)
	case protocol.RecvMoveSummon:
		if summonMoveDiag.Add(1) <= 20 {
			summonDiag("summon_move_in", characterID, body)
		}
		if mv, err := protocol.ReadSummonMove(r); err == nil {
			s.summonMove(channel, live, mv)
		} else {
			summonDiag("summon_move_rejected", characterID, body)
		}
	case protocol.RecvSummonAttack:
		summonDiag("summon_attack_in", characterID, body)
		in, err := protocol.ReadSummonAttack(r)
		if err != nil {
			summonDiag("summon_attack_rejected", characterID, body)
			return nil
		}
		live.longDamage.Apply(in.Targets)
		return s.summonAttackGameplay(ctx, channel, live, sink, in)
	case protocol.RecvDamageSummon:
		summonDiag("summon_damage_in", characterID, body)
		if d, err := protocol.ReadSummonDamage(r); err == nil {
			s.summonDamaged(channel, live, d)
		}
	case protocol.RecvSubSummon:
		summonDiag("summon_sub_in", characterID, body)
		if in, err := protocol.ReadSubSummon(r); err == nil {
			return s.summonSub(channel, live, sink, in)
		}
	case protocol.RecvRemoveSummon:
		if oid, err := protocol.ReadRemoveSummon(r); err == nil {
			return s.cancelSummon(ctx, channel, live, sink, oid)
		}
	}
	return nil
}

func (s *Server) summonCount(key mapKey, ownerID int64, skillID int) int {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	if room := g.rooms[key]; room != nil {
		for _, m := range room.summons {
			if m.ownerID == ownerID && (skillID == 0 || m.source == skillID) {
				n++
			}
		}
	}
	return n
}

// MobTemplate returns the live monster template for a room object (S0a's
// damage triggers use it, e.g. Dark Revenge reflects up to max HP / 10).
func (s *Server) MobTemplate(channel, mapID int, objectID int32) (content.Mob, bool) {
	if s.gameplay == nil {
		return content.Mob{}, false
	}
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	if room := g.rooms[mapKey{channel: channel, mapID: mapID}]; room != nil {
		if m := room.mobs[objectID]; m != nil && m.hp > 0 {
			return m.stats, true
		}
	}
	return content.Mob{}, false
}
