package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const moonRiceCakesToClear = 10

// henesysState serializes planting, drops, escort damage and turn-in for this
// attempt. World and player locks are acquired inside it, never before it.
type henesysState struct {
	mu       sync.Mutex
	cakes    int
	nextCake time.Time
	turnInBy int64
	rewards  map[int64]henesysReward
}

type henesysReward struct {
	exp  int64
	done bool
}

type henesysRoom struct {
	sockets   []*roomReactor
	moon      *roomReactor
	attackers []*gameplayMob
	bunny     *gameplayMob
}

func (g *gameplaySpace) prepareMoonRabbitLocked(room *gameplayRoom) error {
	if room == nil {
		return domain.ErrNotFound
	}
	stats, err := g.catalog.Mob(content.MoonBunny)
	if err != nil {
		return err
	}
	if stats.DropItemPeriod < 1 || stats.DropItemPeriod > 60 {
		return fmt.Errorf("moon rabbit WZ has invalid dropItemPeriod %d", stats.DropItemPeriod)
	}
	// Legacy's spawnMonsterOnGroundBelow lands at the foothold below the
	// full-moon reactor, rather than at the floating art for the moon.
	x := content.HenesysMoon.X
	y, ok := groundBelow(room.definition, x, content.HenesysMoon.Y)
	if !ok {
		return fmt.Errorf("moon rabbit has no foothold")
	}
	life := content.Life{Kind: "m", TemplateID: content.MoonBunny, X: x, Y: y - 1, RX0: x - 80, RX1: x + 80, RespawnSeconds: -1}
	for _, fh := range room.definition.Footholds {
		if min(fh.X1, fh.X2) <= x && x <= max(fh.X1, fh.X2) && fh.X1 != fh.X2 &&
			fh.Y1+(fh.Y2-fh.Y1)*(x-fh.X1)/(fh.X2-fh.X1) == y {
			life.Foothold = fh.ID
			break
		}
	}
	stats.Invincible = true // Player skills cannot damage the escort.
	bunny := &gameplayMob{objectID: g.alloc(), origin: life, life: life, stats: stats, hp: stats.HP}
	if bunny.objectID == 0 || life.Foothold == 0 {
		return fmt.Errorf("unable to prepare moon rabbit spawn")
	}
	state := &henesysRoom{bunny: bunny}
	for oid, mob := range room.mobs {
		if content.IsMoonBunnyAttacker(mob.life.TemplateID) {
			state.attackers = append(state.attackers, mob)
			delete(room.mobs, oid)
		}
	}
	if room.reactors == nil {
		room.reactors = make(map[int32]*roomReactor)
	}
	place := func(def content.Reactor) *roomReactor {
		oid := g.alloc()
		if oid == 0 {
			return nil
		}
		r := &roomReactor{objectID: oid, def: def}
		room.reactors[oid] = r
		return r
	}
	state.moon = place(content.HenesysMoon)
	for _, def := range content.MoonFlowerReactors {
		r := place(def)
		if r == nil {
			return fmt.Errorf("unable to prepare moon flower reactor")
		}
		state.sockets = append(state.sockets, r)
	}
	if state.moon == nil || len(state.attackers) == 0 {
		return fmt.Errorf("unable to prepare moon rabbit encounter")
	}
	room.moon = state
	return nil
}

func (s *Server) prepareHenesysPQ(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	drops, ok := s.repo.(domain.EventDropRepository)
	if !ok {
		return domain.ErrForbidden
	}
	if err := drops.ExpireEventDrops(ctx, s.cfg.WorldID, inst.channel, content.HenesysPQMap); err != nil {
		return err
	}
	// Party quest items carried from earlier attempts cannot plant flowers or
	// count as the new attempt's cakes. Equip rewards stay with the player.
	for _, member := range group {
		err := s.henesysExchange(ctx, member, "henesys-entry-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			var old []domain.ScriptItemDelta
			for _, id := range []int{content.MoonFlowerSeed, content.MoonRiceCake} {
				if count := henesysItemCount(session.Character, id); count > 0 {
					old = append(old, domain.ScriptItemDelta{TemplateID: id, Quantity: -count})
				}
			}
			return domain.ScriptExchange{Items: old}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func henesysItemCount(c domain.Character, itemID int) int {
	count := 0
	for _, item := range c.Items {
		if item.TemplateID == itemID {
			count += item.Quantity
		}
	}
	return count
}

func (s *Server) henesysExchange(ctx context.Context, member eventOccupant, id string,
	makeExchange func(domain.CharacterSession) (domain.ScriptExchange, error)) error {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return domain.ErrForbidden
	}
	member.live.persistMu.Lock()
	defer member.live.persistMu.Unlock()
	session := s.skillSession(member.live)
	exchange, err := makeExchange(session)
	if err != nil {
		return err
	}
	if len(exchange.Items)+len(exchange.Drops)+len(exchange.Logs) == 0 {
		return nil
	}
	exchange.OperationID, exchange.Session = id, session
	result, err := repo.ExecuteScriptExchange(ctx, exchange)
	if err != nil {
		return err
	}
	if exchange.EXP > 0 {
		s.applyProgressResult(member.live, result.Character)
	} else {
		s.applyInventoryResult(member.live, result.Character)
	}
	if !result.Replayed {
		if len(result.Changes) > 0 {
			_ = member.sink.WritePacket(protocol.ModifyInventory(result.Changes))
		}
		for _, item := range result.Drops {
			s.broadcastCombatDrop(item)
		}
		if exchange.EXP > 0 {
			_ = member.sink.WritePacket(protocol.UpdateCombatStats(result.Character))
			_ = member.sink.WritePacket(protocol.ShowExperienceGain(exchange.EXP, true))
		}
	}
	return nil
}

func (s *Server) tickHenesysPQ(ctx context.Context, now time.Time) {
	if s.events == nil {
		return
	}
	s.events.mu.Lock()
	var active []*eventInstance
	for _, inst := range s.events.running {
		if inst.moon != nil && !inst.ended && len(inst.members) >= inst.definition.Keep {
			active = append(active, inst)
		}
	}
	s.events.mu.Unlock()
	for _, inst := range active {
		step, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		if err := s.advanceMoonRabbit(step, inst, now); err != nil && !errors.Is(err, domain.ErrStaleOwner) {
			slog.Warn("moon_rabbit_event_tick", "channel", inst.channel, "error", err)
		}
		cancel()
	}
}

func (s *Server) advanceMoonRabbit(ctx context.Context, inst *eventInstance, now time.Time) error {
	state := inst.moon
	state.mu.Lock()
	defer state.mu.Unlock()
	if !now.Before(inst.deadline) {
		return nil
	}
	key := mapKey{inst.channel, content.HenesysPQMap}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.moon == nil || len(room.members) < inst.definition.Keep {
		g.mu.Unlock()
		return nil
	}
	full := room.moon.moon.state == len(room.moon.sockets)
	bunny := room.moon.bunny
	dropX, dropY := bunny.life.X, bunny.life.Y
	alive := bunny.hp > 0
	g.mu.Unlock()
	if !full {
		return s.plantMoonFlowers(ctx, inst, now, room)
	}
	if !alive || now.Before(state.nextCake) || state.turnInBy != 0 {
		return nil
	}
	// The first WZ interval winds up the pounding animation. The first drop
	// arrives after the second interval, as in the Legacy drop scheduler.
	operation := fmt.Sprintf("henesys-cake-%s-%d", inst.id, state.cakes+1)
	for _, member := range s.eventOccupants(inst.channel, inst.definition.Maps) {
		err := s.henesysExchange(ctx, member, operation, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if session.Character.MapID != key.mapID || session.ChannelID != key.channel ||
				session.Character.HP <= 0 || s.eventOf(session.CharacterID) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{Drops: []domain.ScriptDrop{{TemplateID: content.MoonRiceCake, Quantity: 1, X: dropX, Y: dropY}}}, nil
		})
		if errors.Is(err, domain.ErrStaleOwner) {
			continue
		}
		if err != nil {
			return err
		}
		state.cakes++
		state.nextCake = now.Add(time.Duration(bunny.stats.DropItemPeriod) * time.Second)
		return nil
	}
	return nil
}

func (s *Server) plantMoonFlowers(ctx context.Context, inst *eventInstance, now time.Time, room *gameplayRoom) error {
	reader, readable := s.repo.(domain.InventoryRepository)
	writer, writable := s.repo.(domain.EventDropRepository)
	if !readable || !writable {
		return domain.ErrForbidden
	}
	mapID := content.HenesysPQMap
	list, err := reader.ListItemDrops(ctx, s.cfg.WorldID, inst.channel, mapID)
	if err != nil {
		return err
	}
	for _, d := range list {
		if d.TemplateID != content.MoonFlowerSeed || d.Quantity != 1 || !d.PlayerDrop || s.eventOf(d.OwnerID) != inst {
			continue
		}
		var socket *roomReactor
		s.gameplay.mu.Lock()
		for _, candidate := range room.moon.sockets {
			if candidate.state == 0 && content.MoonFlowerSeedFits(candidate.def, d.X, d.Y) {
				socket = candidate
				break
			}
		}
		s.gameplay.mu.Unlock()
		if socket == nil {
			continue
		}
		applied, err := writer.ConsumeEventDrop(ctx, d)
		if err != nil {
			return err
		}
		if !applied {
			continue
		}
		s.removeVisibleDrop(d, protocol.RemoveItemExpired(d.ObjectID))
		s.activateMoonFlower(inst, now, room, socket)
	}
	return nil
}

func (s *Server) activateMoonFlower(inst *eventInstance, now time.Time, room *gameplayRoom, socket *roomReactor) {
	packets := make([]roomDelivery, 0, len(room.moon.attackers)*2)
	g := s.gameplay
	g.mu.Lock()
	if g.rooms[mapKey{inst.channel, content.HenesysPQMap}] != room {
		g.mu.Unlock()
		return
	}
	socket.state = 1
	moon := room.moon.moon
	moon.state++
	packets = append(packets, roomBroadcast(room, protocol.ReactorState(socket.objectID, socket.state, socket.def.X, socket.def.Y), -1)...)
	packets = append(packets, roomBroadcast(room, protocol.ReactorState(moon.objectID, moon.state, moon.def.X, moon.def.Y), -1)...)
	if moon.state == len(room.moon.sockets) {
		bunny := room.moon.bunny
		inst.moon.nextCake = now.Add(2 * time.Duration(bunny.stats.DropItemPeriod) * time.Second)
		spawns := append(append([]*gameplayMob(nil), room.moon.attackers...), bunny)
		for _, mob := range spawns {
			room.mobs[mob.objectID] = mob
			packets = append(packets, roomBroadcast(room, mob.spawnPacket(false, now), -1)...)
			for id, member := range room.members {
				mob.controller = id
				packets = append(packets, roomDelivery{member.sink, mob.spawnPacket(true, now)})
				break
			}
		}
		packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage("月妙出現了！保護牠並收集 10 個元宵。"), -1)...)
	}
	g.mu.Unlock()
	deliver(packets)
}

func (s *Server) moonBunnyHit(channel int, live *liveCharacter, sink packetSink, hit protocol.MobHitEscort, now time.Time) {
	session := partySession(live)
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.moon == nil || inst.channel != channel || session.CharacterID != int64(hit.CharacterID) ||
		session.Character.MapID != content.HenesysPQMap || session.Character.HP <= 0 {
		return
	}
	inst.moon.mu.Lock()
	defer inst.moon.mu.Unlock()
	if inst.moon.turnInBy != 0 || s.eventOf(session.CharacterID) != inst {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, content.HenesysPQMap}]
	if room == nil || room.moon == nil {
		g.mu.Unlock()
		return
	}
	member := room.members[session.CharacterID]
	src, dst := room.mobs[hit.Attacker], room.mobs[hit.Escort]
	if member == nil || member.live != live || !sameDropSink(member.sink, sink) ||
		src == nil || dst == nil || room.moon.bunny != dst || src.hp <= 0 || dst.hp <= 0 ||
		src.claiming || src.controller != session.CharacterID || !content.IsMoonBunnyAttacker(src.life.TemplateID) ||
		!within(src.life.X, src.life.Y, dst.life.X, dst.life.Y, 130) || now.Sub(src.escortHitAt) < 3*time.Second {
		g.mu.Unlock()
		return
	}
	src.escortHitAt = now
	damage := min(dst.hp, int64(max(1, src.stats.PAD)))
	dst.hp -= damage
	dead := dst.hp == 0
	packets := roomBroadcast(room, protocol.DamageEscort(dst.objectID, int(damage), int(dst.hp), int(dst.stats.HP)), -1)
	if dead {
		packets = append(packets, roomBroadcast(room, protocol.KillMonster(dst.objectID), -1)...)
		packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage("月妙倒下了，挑戰失敗。"), -1)...)
	}
	g.mu.Unlock()
	deliver(packets)
	if dead {
		s.eventEnd(inst)
	}
}

func henesysClearEXP(level int) int64 {
	// Legacy givePartyExp_PQ(70, 1.5), using the current world's EXP table.
	scaled := level
	if level > 70 {
		scaled = 70 + level/10
	}
	denominator := int64(max(1, min(level, 70)) * 3)
	return (content.ExperienceForLevel(scaled)*20 + denominator/2) / denominator
}

func (s *Server) finishMoonRabbit(ctx context.Context, inst *eventInstance, actor domain.CharacterSession) string {
	state := inst.moon
	state.mu.Lock()
	defer state.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || actor.Character.HP <= 0 || !time.Now().Before(inst.deadline) {
		return "unavailable"
	}
	if state.cakes < moonRiceCakesToClear || state.turnInBy == 0 &&
		henesysItemCount(actor.Character, content.MoonRiceCake) < moonRiceCakesToClear {
		return "cakes"
	}
	members := s.eventOccupants(inst.channel, inst.definition.Maps)
	if state.turnInBy == 0 {
		rewards := make(map[int64]henesysReward)
		for _, m := range members {
			peer := partySession(m.live)
			if s.eventOf(peer.CharacterID) == inst {
				rewards[peer.CharacterID] = henesysReward{exp: henesysClearEXP(peer.Character.Level)}
			}
		}
		if len(rewards) < inst.definition.Keep {
			return "unavailable"
		}
		// Record all stable reward amounts before the first save. The durable
		// operation ID is shared among participants, scoped by character ID.
		state.rewards = rewards
	}
	actorFirst := append([]eventOccupant(nil), members...)
	for i, m := range actorFirst {
		if partySession(m.live).CharacterID == actor.CharacterID {
			actorFirst[0], actorFirst[i] = actorFirst[i], actorFirst[0]
			break
		}
	}
	for _, m := range actorFirst {
		id := partySession(m.live).CharacterID
		reward, ok := state.rewards[id]
		if !ok || reward.done {
			continue
		}
		err := s.henesysExchange(ctx, m, "henesys-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(id) != inst || session.Character.MapID != content.HenesysPQMap {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			x := domain.ScriptExchange{
				EXP:  reward.exp,
				Logs: []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "henesys-clear"}, Delta: 1}},
			}
			if state.turnInBy == id || state.turnInBy == 0 && id == actor.CharacterID {
				x.Items = []domain.ScriptItemDelta{{TemplateID: content.MoonRiceCake, Quantity: -moonRiceCakesToClear}}
			}
			return x, nil
		})
		if err != nil {
			slog.Warn("moon_rabbit_reward", "channel", inst.channel, "error", err)
			return "unavailable"
		}
		if state.turnInBy == 0 && id == actor.CharacterID {
			state.turnInBy = id
		}
		reward.done = true
		state.rewards[id] = reward
	}
	s.broadcastInventoryPacket(inst.channel, content.HenesysPQMap, protocol.FieldScreenEffect(eventEffects["clear"][0]))
	s.broadcastInventoryPacket(inst.channel, content.HenesysPQMap, protocol.FieldSoundEffect(eventEffects["clear"][1]))
	s.eventEndAt(inst, content.HenesysPQExit)
	return "ok"
}
