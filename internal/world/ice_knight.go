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

type iceEscortRoom struct {
	mob       *gameplayMob
	next      int
	done      bool
	lastAt    time.Time
	stopUntil time.Time
}

func (room *iceEscortRoom) pathPacket(route []content.EscortNode) []byte {
	mob := room.mob
	return protocol.EscortPath(mob.objectID, mob.life.X, mob.life.Y, room.next, route, int(time.Until(room.stopUntil).Milliseconds()))
}

func (g *gameplaySpace) prepareIceEscort(inst *eventInstance, hp int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, mapID := range []int{content.IceKnightFirst, content.IceKnightSecond} {
		room := g.rooms[mapKey{inst.channel, mapID}]
		if room == nil || len(room.definition.EscortRoute) < 2 || hp <= 0 {
			return domain.ErrInvalidInput
		}
		for _, mob := range room.mobs {
			if mob.life.TemplateID == content.IceEscortMob {
				mob.stats.HP, mob.hp = hp, hp
				mob.stats.Invincible = true
				room.ice = &iceEscortRoom{mob: mob}
				break
			}
		}
		if room.ice == nil {
			return domain.ErrInvalidInput
		}
	}
	return nil
}

func (s *Server) iceEscortPath(channel int, live *liveCharacter, sink packetSink, oid int32) error {
	actor := partySession(live)
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ice == nil || inst.channel != channel {
		return nil
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, actor.Character.MapID}]
	if room == nil || room.ice == nil || room.ice.done || room.ice.mob.objectID != oid ||
		room.ice.mob.controller != actor.CharacterID || room.members[actor.CharacterID] == nil {
		g.mu.Unlock()
		return nil
	}
	p := room.ice.pathPacket(room.definition.EscortRoute)
	g.mu.Unlock()
	return sink.WritePacket(p)
}

func (s *Server) iceEscortNode(channel int, live *liveCharacter, sink packetSink, oid int32, next int) {
	actor := partySession(live)
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ice == nil || inst.channel != channel {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, actor.Character.MapID}]
	if room == nil || room.ice == nil {
		g.mu.Unlock()
		return
	}
	stage, route := room.ice, room.definition.EscortRoute
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
		}
		g.mu.Unlock()
		return
	}
	stage.done = true
	delete(room.mobs, oid)
	packets := roomBroadcast(room, protocol.KillMonster(oid), -1)
	packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage("冰人已安全抵達，請前往下一關。"), -1)...)
	g.mu.Unlock()
	deliver(packets)
}

func (s *Server) resumeIceEscorts(now time.Time) {
	g := s.gameplay
	var packets []roomDelivery
	g.mu.Lock()
	for _, room := range g.rooms {
		stage := room.ice
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

func (s *Server) iceEscortHit(channel int, live *liveCharacter, sink packetSink, hit protocol.MobHitEscort, now time.Time) {
	actor := partySession(live)
	if int64(hit.CharacterID) != actor.CharacterID {
		return
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ice == nil || inst.channel != channel {
		return
	}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[mapKey{channel, actor.Character.MapID}]
	if room == nil || room.ice == nil || room.ice.done {
		g.mu.Unlock()
		return
	}
	member := room.members[actor.CharacterID]
	src, dst := room.mobs[hit.Attacker], room.ice.mob
	if member == nil || member.live != live || !sameDropSink(member.sink, sink) || src == nil ||
		src.hp <= 0 || src.claiming || dst.hp <= 0 || hit.Escort != dst.objectID ||
		(src.life.TemplateID != 9300439 && src.life.TemplateID != 9300440) ||
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
		packets = append(packets, roomBroadcast(room, protocol.PortalTrainingMessage("冰人已倒下，這次挑戰結束了。"), -1)...)
	}
	g.mu.Unlock()
	deliver(packets)
	if dead {
		s.eventEnd(inst)
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
	ready := room != nil && room.ice != nil && room.ice.done
	if ready && next == content.IceKnightSecond {
		g.rooms[mapKey{inst.channel, next}].ice.mob.hp = room.ice.mob.hp
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
	if s.eventOf(id) != inst || !time.Now().Before(inst.deadline) || s.mapMonsterCount(inst.channel, content.IceKnightArena) != 0 {
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
