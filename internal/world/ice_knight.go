package world

import (
	"context"
	"sync"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type iceKnightState struct {
	mu      sync.Mutex
	rewards map[int64]int64
}

type escortRoom struct {
	mob       *gameplayMob
	next      int
	done      bool
	lastAt    time.Time
	stopUntil time.Time
}

type eventBossForms struct {
	active  *gameplayMob
	next    []content.Mob
	expires time.Time
	done    bool
}

func (room *escortRoom) pathPacket(route []content.EscortNode) []byte {
	mob := room.mob
	return protocol.EscortPath(mob.objectID, mob.life.X, mob.life.Y, room.next, route, int(time.Until(room.stopUntil).Milliseconds()))
}

func (g *gameplaySpace) prepareIceEscort(inst *eventInstance, hp int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, mapID := range []int{content.IceKnightFirst, content.IceKnightSecond} {
		if err := prepareEscortRoom(g.rooms[mapKey{inst.channel, mapID}], content.IceEscortMob, hp); err != nil {
			return err
		}
	}
	return g.prepareIceArena(g.rooms[mapKey{inst.channel, content.IceKnightArena}])
}

func prepareEscortRoom(room *gameplayRoom, template int, hp int64) error {
	if room == nil || len(room.definition.EscortRoute) < 2 || hp <= 0 {
		return domain.ErrInvalidInput
	}
	for _, mob := range room.mobs {
		if mob.life.TemplateID == template {
			mob.stats.HP, mob.hp = hp, hp
			mob.stats.Invincible = true
			room.escort = &escortRoom{mob: mob}
			return nil
		}
	}
	return domain.ErrInvalidInput
}

func (g *gameplaySpace) prepareIceArena(room *gameplayRoom) error {
	if room == nil {
		return domain.ErrInvalidInput
	}
	for _, mob := range room.mobs {
		if mob.life.TemplateID != content.IceKnightBoss {
			continue
		}
		if len(mob.stats.Revive) != 1 || mob.stats.Revive[0] != 9300442 {
			return domain.ErrInvalidInput
		}
		knight, err := g.catalog.Mob(mob.stats.Revive[0])
		if err != nil || knight.HP <= 0 || knight.Invincible || len(knight.Revive) != 1 || knight.Revive[0] != 9300465 {
			return domain.ErrInvalidInput
		}
		ending, err := g.catalog.Mob(knight.Revive[0])
		if err != nil || ending.HP <= 0 || !ending.Invincible || ending.RemoveAfter <= 0 {
			return domain.ErrInvalidInput
		}
		room.bossForms = &eventBossForms{active: mob, next: []content.Mob{knight, ending}}
		return nil
	}
	return domain.ErrInvalidInput
}

func (s *Server) escortPath(channel int, live *liveCharacter, sink packetSink, oid int32) error {
	actor := partySession(live)
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || (inst.ice == nil && inst.hoblin == nil) || inst.channel != channel {
		return nil
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, actor.Character.MapID}]
	if room == nil || room.escort == nil || room.escort.done || room.escort.mob.objectID != oid ||
		room.escort.mob.controller != actor.CharacterID || room.members[actor.CharacterID] == nil {
		g.mu.Unlock()
		return nil
	}
	p := room.escort.pathPacket(room.definition.EscortRoute)
	g.mu.Unlock()
	return sink.WritePacket(p)
}

func (s *Server) escortNode(channel int, live *liveCharacter, sink packetSink, oid int32, next int) {
	actor := partySession(live)
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || (inst.ice == nil && inst.hoblin == nil) || inst.channel != channel {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, actor.Character.MapID}]
	if room == nil || room.escort == nil {
		g.mu.Unlock()
		return
	}
	stage, route := room.escort, room.definition.EscortRoute
	member := room.members[actor.CharacterID]
	now := time.Now()
	if member == nil || member.live != live || !sameDropSink(member.sink, sink) || stage.done || stage.mob.hp <= 0 ||
		stage.mob.objectID != oid || stage.mob.controller != actor.CharacterID || next != stage.next+1 || next > len(route) ||
		!stage.stopUntil.IsZero() || now.Sub(stage.lastAt) < 100*time.Millisecond {
		g.mu.Unlock()
		return
	}
	node := route[next-1]
	if !within(stage.mob.life.X, stage.mob.life.Y, node.X, node.Y, 150) {
		g.mu.Unlock()
		return
	}
	stage.next, stage.lastAt = next, now
	if next != len(route) {
		if node.Attribute == 2 && node.StopMillis > 0 {
			stage.stopUntil = now.Add(time.Duration(node.StopMillis) * time.Millisecond)
			if room.rex != nil && node.Key == 4 && room.rex.summonAt.IsZero() {
				room.rex.summonAt = now.Add(1500 * time.Millisecond)
			}
		}
		g.mu.Unlock()
		return
	}
	stage.done = true
	delete(room.mobs, oid)
	packets := roomBroadcast(room, protocol.KillMonster(oid), -1)
	notice := "冰人已安全抵達，請前往下一關。"
	if inst.hoblin != nil {
		notice = "邪摩斯已安全抵達，請前往下一關。"
	}
	packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage(notice), -1)...)
	g.mu.Unlock()
	deliver(packets)
}

func (s *Server) tickEscortEvents(now time.Time) {
	g := s.gameplay
	var packets []roomDelivery
	g.mu.Lock()
	for _, room := range g.rooms {
		if room.rex != nil && len(room.members) != 0 {
			packets = append(packets, g.summonRex(room, now)...)
		}
		if room.bossForms != nil && len(room.members) != 0 {
			packets = append(packets, g.advanceEventBossForms(room, now)...)
		}
		stage := room.escort
		if stage == nil || stage.stopUntil.IsZero() || now.Before(stage.stopUntil) {
			continue
		}
		stage.stopUntil = time.Time{}
		if stage.mob.hp > 0 && !stage.done {
			packets = append(packets, roomBroadcast(room, protocol.ResumeEscort(stage.mob.objectID), -1)...)
		}
	}
	g.mu.Unlock()
	deliver(packets)
}

func (g *gameplaySpace) advanceEventBossForms(room *gameplayRoom, now time.Time) []roomDelivery {
	boss := room.bossForms
	if boss.done {
		return nil
	}
	if !boss.expires.IsZero() {
		if now.Before(boss.expires) {
			return nil
		}
		delete(room.mobs, boss.active.objectID)
		boss.done = true
		return roomBroadcast(room, protocol.KillMonster(boss.active.objectID), -1)
	}
	if boss.active.hp > 0 || boss.active.claiming || len(boss.next) == 0 {
		return nil
	}
	stats := boss.next[0]
	life := boss.active.life
	mob, packets := g.spawnEventBossForm(room, life, stats, boss.active.controller, now)
	if mob == nil {
		return nil
	}
	boss.active, boss.next = mob, boss.next[1:]
	if stats.RemoveAfter > 0 {
		boss.expires = now.Add(time.Duration(stats.RemoveAfter) * time.Second)
	}
	return packets
}

func (g *gameplaySpace) spawnEventBossForm(room *gameplayRoom, life content.Life, stats content.Mob, controller int64, now time.Time) (*gameplayMob, []roomDelivery) {
	oid := g.alloc()
	if oid == 0 {
		return nil, nil
	}
	life.TemplateID = stats.ID
	if room.members[controller] == nil {
		for id := range room.members {
			controller = id
			break
		}
	}
	mob := &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP, controller: controller}
	room.mobs[oid] = mob
	packets := roomBroadcast(room, mob.spawnPacket(false, now), -1)
	if member := room.members[controller]; member != nil {
		packets = append(packets, roomDelivery{member.sink, mob.spawnPacket(true, now)})
	}
	return mob, packets
}

func (s *Server) escortHit(channel int, live *liveCharacter, sink packetSink, hit protocol.MobHitEscort, now time.Time) {
	actor := partySession(live)
	if int64(hit.CharacterID) != actor.CharacterID {
		return
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || (inst.ice == nil && inst.hoblin == nil) || inst.channel != channel {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	key := mapKey{channel, actor.Character.MapID}
	room := g.rooms[key]
	if room == nil || room.escort == nil || room.escort.done {
		g.mu.Unlock()
		return
	}
	member := room.members[actor.CharacterID]
	src, dst := room.mobs[hit.Attacker], room.escort.mob
	if member == nil || member.live != live || !sameDropSink(member.sink, sink) || src == nil ||
		src.hp <= 0 || src.claiming || dst.hp <= 0 || hit.Escort != dst.objectID ||
		!escortThreat(src.life.TemplateID, dst.life.TemplateID) ||
		!within(src.life.X, src.life.Y, dst.life.X, dst.life.Y, 130) || now.Sub(src.escortHitAt) < 3*time.Second {
		g.mu.Unlock()
		return
	}
	src.escortHitAt = now
	damage := min(dst.hp, int64(max(1, src.stats.PAD)))
	dst.hp -= damage
	dead := dst.hp == 0
	if dead && inst.hoblin != nil {
		g.mu.Unlock()
		if s.rescueHoblinEscort(inst, key, room, dst, actor.CharacterID) {
			return
		}
		g.mu.Lock()
		if g.rooms[key] != room {
			g.mu.Unlock()
			return
		}
	}
	packets := roomBroadcast(room, protocol.DamageEscort(dst.objectID, int(damage), int(dst.hp), int(dst.stats.HP)), -1)
	if dead {
		packets = append(packets, roomBroadcast(room, protocol.KillMonster(dst.objectID), -1)...)
		notice := "冰人已倒下，這次挑戰結束了。"
		if inst.hoblin != nil {
			notice = "邪摩斯已倒下，這次挑戰結束了。"
		}
		packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage(notice), -1)...)
	}
	g.mu.Unlock()
	deliver(packets)
	if dead {
		s.eventEnd(inst)
	}
}

func escortThreat(attacker, escort int) bool {
	switch escort {
	case content.IceEscortMob:
		return attacker == 9300439 || attacker == 9300440
	case content.HoblinEscort, content.HoblinArenaEscort:
		return (attacker >= 9300276 && attacker <= 9300280) || attacker == content.HoblinKing
	default:
		return false
	}
}

func (s *Server) iceKnightPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	actor := partySession(live)
	roomID := actor.Character.MapID
	if roomID == content.IceKnightResult && p.Script == "iceman_Revive" {
		return true, s.travelFromIceKnight(ctx, channel, live, sink)
	}
	if p.Script != "iceman_Next" || (roomID != content.IceKnightFirst && roomID != content.IceKnightSecond) {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ice == nil || s.partySnapshotOf(actor.CharacterID).LeaderID != actor.CharacterID {
		return true, eventPortalNotice(sink, "請讓隊長開啟傳送點。")
	}
	inst.ice.mu.Lock()
	defer inst.ice.mu.Unlock()
	if s.eventOf(actor.CharacterID) != inst || !time.Now().Before(inst.deadline) || partySession(live).Character.MapID != roomID ||
		len(s.activeEventMembers(inst, roomID)) < inst.definition.Keep {
		return true, eventPortalNotice(sink, "請等全隊抵達再一起前進。")
	}
	next := roomID + 100
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{inst.channel, roomID}]
	ready := room != nil && room.escort != nil && room.escort.done
	if ready && next == content.IceKnightSecond {
		g.rooms[mapKey{inst.channel, next}].escort.mob.hp = room.escort.mob.hp
	}
	g.mu.Unlock()
	if !ready {
		return true, eventPortalNotice(sink, "請先護送冰人到出口。")
	}
	m, err := g.catalog.Map(next)
	if err != nil {
		return true, err
	}
	entry, ok := m.Spawn()
	if !ok {
		return true, domain.ErrInvalidInput
	}
	return true, s.moveEventRoom(ctx, inst, roomID, eventOccupant{live, sink}, next, entry)
}

func (s *Server) travelFromIceKnight(ctx context.Context, channel int, live *liveCharacter, sink packetSink) error {
	m, err := s.gameplay.catalog.Map(content.IceKnightLobby)
	if err != nil {
		return err
	}
	entry, ok := m.Spawn()
	if !ok {
		return domain.ErrInvalidInput
	}
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	return s.warpGameplay(ctx, channel, live, sink, m.ID, entry)
}

func (s *Server) finishIceKnight(ctx context.Context, actor domain.CharacterSession) (bool, error) {
	id := actor.CharacterID
	inst := s.eventOf(id)
	if inst == nil || inst.ice == nil || actor.Character.MapID != content.IceKnightArena || s.partySnapshotOf(id).LeaderID != id {
		return false, nil
	}
	inst.ice.mu.Lock()
	defer inst.ice.mu.Unlock()
	if s.eventOf(id) != inst || !time.Now().Before(inst.deadline) {
		return false, nil
	}
	s.gameplay.mu.Lock()
	room := s.gameplay.rooms[mapKey{inst.channel, content.IceKnightArena}]
	done := room != nil && room.bossForms != nil && room.bossForms.done
	s.gameplay.mu.Unlock()
	if !done {
		return false, nil
	}
	group := s.activeEventMembers(inst, content.IceKnightArena)
	if len(group) < inst.definition.Keep {
		return false, nil
	}
	for _, member := range group {
		peer := partySession(member.live)
		if _, ok := inst.ice.rewards[peer.CharacterID]; !ok {
			level := max(1, peer.Character.Level)
			scaled := level
			if level > 70 {
				scaled = 70 + level/10
			}
			denom := int64(min(70, level))
			inst.ice.rewards[peer.CharacterID] = (content.ExperienceForLevel(scaled)*10 + denom/2) / denom
		}
		err := s.applyEventExchange(ctx, member, "ice-knight-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(session.CharacterID) != inst || session.Character.MapID != content.IceKnightArena {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			return domain.ScriptExchange{
				EXP:   inst.ice.rewards[peer.CharacterID],
				Items: []domain.ScriptItemDelta{{TemplateID: content.IceKnightFrost, Quantity: 1}},
				Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "ice-knight-clear"}, Delta: 1}},
			}, nil
		})
		if err != nil {
			return false, err
		}
	}
	s.broadcastInventoryPacket(inst.channel, content.IceKnightArena, protocol.FieldScreenEffect(eventEffects["clear"][0]))
	s.broadcastInventoryPacket(inst.channel, content.IceKnightArena, protocol.FieldSoundEffect(eventEffects["clear"][1]))
	s.eventEndAt(inst, content.IceKnightResult)
	return true, nil
}
