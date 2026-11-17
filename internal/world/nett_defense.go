package world

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const (
	nettBarrierHP  = 20
	nettDailyLimit = 5
	nettWavePause  = 4 * time.Second
	nettResultTime = 4 * time.Second
)

var nettDaily = domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogDaily, Name: "nett_defense_entries"}
var nettEmeraldDue = domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "nett_defense_emeralds"}

// Nett's four lanes and wave schedule are shared by the room. All fields below
// are changed under gameplay.mu, including the lane mob positions.
type nettDefenseRoom struct {
	spawns   map[int]content.Mob
	wave     int
	next     int
	life     int
	waveAt   time.Time
	lastStep time.Time
	finishAt time.Time
}

func (g *gameplaySpace) prepareNettDefenseLocked(room *gameplayRoom) error {
	plan := room.definition.NettDefense
	if plan == nil {
		return fmt.Errorf("field %d is missing the v148 Nett defense schedule", room.definition.ID)
	}
	state := &nettDefenseRoom{life: nettBarrierHP, spawns: make(map[int]content.Mob)}
	for i, lane := range plan.Lanes {
		if _, ok := nettGround(room.definition, lane.X, lane.Y); !ok || lane.X <= plan.ObeliskX {
			return fmt.Errorf("field %d has no foothold for Nett lane %d", room.definition.ID, i)
		}
	}
	for _, wave := range plan.Waves {
		for _, spec := range wave {
			if _, known := state.spawns[spec.MobID]; known {
				continue
			}
			mob, err := g.catalog.Mob(spec.MobID)
			if err != nil || mob.DefenseLifePoint < 1 {
				return fmt.Errorf("field %d has invalid defense mob %d: %v", room.definition.ID, spec.MobID, err)
			}
			state.spawns[spec.MobID] = mob
		}
	}
	room.nett = state
	return nil
}

func nettGround(field content.Map, x, y int) (content.Foothold, bool) {
	bestDelta := 28
	var best content.Foothold
	for _, fh := range field.Footholds {
		if fh.X1 == fh.X2 || x < min(fh.X1, fh.X2) || x > max(fh.X1, fh.X2) {
			continue
		}
		ground := nettGroundY(fh, x)
		if d := ground - y; d >= 0 && d < bestDelta {
			bestDelta, best = d, fh
		}
	}
	return best, bestDelta < 28
}

func nettGroundY(fh content.Foothold, x int) int {
	return fh.Y1 + (fh.Y2-fh.Y1)*(x-fh.X1)/(fh.X2-fh.X1)
}

func (s *Server) nettGroupLog(ctx context.Context, inst *eventInstance, group []eventOccupant, action string, change domain.ScriptLogChange) error {
	repo, ok := s.repo.(domain.EventLogRepository)
	if !ok || len(group) != content.NettLanes {
		return domain.ErrForbidden
	}
	slices.SortFunc(group, func(a, b eventOccupant) int {
		return cmp.Compare(partySession(a.live).CharacterID, partySession(b.live).CharacterID)
	})
	for _, member := range group {
		member.live.persistMu.Lock()
		defer member.live.persistMu.Unlock()
	}
	all := make([]domain.ScriptExchange, 0, len(group))
	for _, member := range group {
		session := s.skillSession(member.live)
		if session.ChannelID != inst.channel || session.Character.MapID != inst.definition.Maps[0] ||
			session.Character.HP <= 0 || s.eventOf(session.CharacterID) != inst {
			return domain.ErrStaleOwner
		}
		all = append(all, domain.ScriptExchange{Session: session,
			OperationID: "event-" + inst.id + "-nett-" + action, Logs: []domain.ScriptLogChange{change}})
	}
	results, err := repo.ExecuteEventLogs(ctx, all)
	if err != nil {
		return err
	}
	for i, result := range results {
		value := result.Character
		// A roster log increments the revision without changing vitals. Keep
		// combat damage and regeneration newer than the latest SQL checkpoint.
		value.HP, value.MP = all[i].Session.Character.HP, all[i].Session.Character.MP
		s.applyInventoryResult(group[i].live, value)
	}
	return nil
}

func (s *Server) nettCanEnter(ctx context.Context, group []eventOccupant) bool {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok || len(group) != content.NettLanes {
		return false
	}
	for _, member := range group {
		logs, err := repo.ReadScriptLogs(ctx, s.skillSession(member.live), []domain.ScriptLogKey{nettDaily})
		if err != nil || len(logs) != 1 || logs[0].Value >= nettDailyLimit {
			return false
		}
	}
	return true
}

func (s *Server) startNettDefense(ctx context.Context, inst *eventInstance) error {
	members := s.activeEventMembers(inst, inst.definition.Maps[0])
	dailyMax := int64(nettDailyLimit)
	if err := s.nettGroupLog(ctx, inst, members, "entry", domain.ScriptLogChange{Key: nettDaily, Delta: 1, Maximum: &dailyMax}); err != nil {
		return err
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{inst.channel, inst.definition.Maps[0]}]
	now := time.Now()
	room.nett.wave = 1
	room.nett.waveAt = now.Add(nettWavePause)
	room.nett.lastStep = now
	packets := roomBroadcast(room, protocol.NettDefenseLife(room.nett.life), -1)
	packets = append(packets, roomBroadcast(room, protocol.NettDefenseWave(room.nett.wave), -1)...)
	packets = append(packets, roomBroadcast(room, protocol.NettDefensePoints(0), -1)...)
	g.mu.Unlock()
	deliver(packets)
	return nil
}

func (s *Server) tickNettDefense(ctx context.Context, now time.Time) {
	if s.events == nil {
		return
	}
	s.events.mu.Lock()
	var instances []*eventInstance
	for _, inst := range s.events.running {
		if content.NettEvent(inst.definition.Name) && !inst.starting && !inst.ended {
			instances = append(instances, inst)
		}
	}
	s.events.mu.Unlock()
	for _, inst := range instances {
		step, cancel := context.WithTimeout(ctx, time.Second)
		s.advanceNettDefense(step, inst, now)
		cancel()
	}
}

func (s *Server) advanceNettDefense(ctx context.Context, inst *eventInstance, now time.Time) {
	key := mapKey{inst.channel, inst.definition.Maps[0]}
	if len(s.activeEventMembers(inst, key.mapID)) != content.NettLanes {
		s.eventEnd(inst)
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.nett == nil || room.nett.wave == 0 {
		g.mu.Unlock()
		return
	}
	state := room.nett
	if !state.finishAt.IsZero() {
		done := !now.Before(state.finishAt)
		g.mu.Unlock()
		if done {
			s.eventEnd(inst)
		}
		return
	}
	packets, waveFinished := g.stepNettDefenseLocked(room, now)
	if state.life == 0 || waveFinished && state.wave == content.NettWaves {
		clear := state.life > 0
		g.mu.Unlock()
		if clear {
			group := s.activeEventMembers(inst, key.mapID)
			if err := s.nettGroupLog(ctx, inst, group, "clear", domain.ScriptLogChange{Key: nettEmeraldDue, Delta: content.NettWaves}); err != nil {
				deliver(packets)
				return
			}
		}
		g.mu.Lock()
		state.finishAt = now.Add(nettResultTime)
		packets = append(packets, roomBroadcast(room, protocol.NettDefenseResult(clear, state.wave, state.life, 0, 0), -1)...)
		g.mu.Unlock()
		deliver(packets)
		return
	}
	if waveFinished {
		state.wave++
		state.next = 0
		state.waveAt = now.Add(nettWavePause)
		packets = append(packets, roomBroadcast(room, protocol.NettDefenseWave(state.wave), -1)...)
	}
	g.mu.Unlock()
	deliver(packets)
}

func (g *gameplaySpace) stepNettDefenseLocked(room *gameplayRoom, now time.Time) ([]roomDelivery, bool) {
	state, plan := room.nett, room.definition.NettDefense
	elapsed := min(max(now.Sub(state.lastStep), 0), time.Second)
	state.lastStep = now
	packets := g.advanceNettMonstersLocked(room, elapsed, int(elapsed.Milliseconds()))
	if state.life == 0 {
		return packets, false
	}
	wave := plan.Waves[state.wave-1]
	for state.next < len(wave) && !now.Before(state.waveAt.Add(wave[state.next].After)) {
		spawn := wave[state.next]
		lane := plan.Lanes[spawn.Lane]
		fh, _ := nettGround(room.definition, lane.X, lane.Y)
		life := content.Life{Kind: "m", TemplateID: spawn.MobID, X: lane.X, Y: nettGroundY(fh, lane.X) - 1, Foothold: fh.ID,
			RX0: plan.ObeliskX, RX1: lane.X, RespawnSeconds: -1}
		stats := state.spawns[spawn.MobID]
		if oid := g.alloc(); oid > 0 {
			mob := &gameplayMob{objectID: oid, origin: life, life: life, stats: stats, hp: stats.HP, oneTime: true, nettLane: &lane}
			room.mobs[oid] = mob
			packets = append(packets, roomBroadcast(room, mob.spawnPacket(false, now), -1)...)
		} else {
			state.life = 0
			return packets, false
		}
		state.next++
	}
	return packets, state.next == len(wave) && len(room.mobs) == 0
}

func (g *gameplaySpace) advanceNettMonstersLocked(room *gameplayRoom, elapsed time.Duration, millis int) []roomDelivery {
	if millis <= 0 {
		return nil
	}
	state, plan := room.nett, room.definition.NettDefense
	var packets []roomDelivery
	before := state.life
	for oid, mob := range room.mobs {
		if mob.nettLane == nil || mob.claiming || mob.reservation != nil {
			continue
		}
		if mob.hp <= 0 {
			delete(room.mobs, oid)
			continue
		}
		oldX := mob.life.X
		speed := max(120+mob.stats.DefenseSpeed, 20)
		x := max(plan.ObeliskX, oldX-int(elapsed*time.Duration(speed)/time.Second))
		if x <= plan.ObeliskX {
			state.life = max(0, state.life-mob.stats.DefenseLifePoint)
			delete(room.mobs, oid)
			packets = append(packets, roomBroadcast(room, protocol.RemoveMonster(oid), -1)...)
			continue
		}
		fh, ok := nettGround(room.definition, x, mob.nettLane.Y)
		if !ok {
			// Each lane ends at its gate to the left of the last foothold.
			state.life = max(0, state.life-mob.stats.DefenseLifePoint)
			delete(room.mobs, oid)
			packets = append(packets, roomBroadcast(room, protocol.RemoveMonster(oid), -1)...)
			continue
		}
		y := nettGroundY(fh, x)
		packets = append(packets, roomBroadcast(room, protocol.NettDefenseWalk(oid, oldX, x, y, fh.ID, millis), -1)...)
		mob.life.X, mob.life.Y, mob.life.Foothold = x, y-1, fh.ID
	}
	if state.life != before {
		packets = append(packets, roomBroadcast(room, protocol.NettDefenseLife(state.life), -1)...)
	}
	return packets
}

func (s *Server) nettLanePortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	actor := partySession(live)
	if !content.NettDefenseMap(actor.Character.MapID) || portal.Type != 1 ||
		!strings.HasPrefix(portal.Name, "up") && !strings.HasPrefix(portal.Name, "down") {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || !content.NettEvent(inst.definition.Name) || inst.channel != channel ||
		actor.Character.MapID != portal.TargetMap {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	field, err := s.gameplay.catalog.Map(actor.Character.MapID)
	if err != nil {
		return true, err
	}
	entry, ok := field.Portal(portal.TargetName)
	if !ok {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	return true, s.warpEventMember(ctx, inst, actor.Character.MapID, eventOccupant{live, sink}, field.ID, entry)
}
