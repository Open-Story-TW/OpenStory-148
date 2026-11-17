package world

import (
	"context"
	"errors"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const kentaDefenseTime = 3 * time.Minute

func (s *Server) kentaPortal(ctx context.Context, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	session := partySession(live)
	switch portal.Script {
	case "kenta_out00":
		inst := s.eventOf(session.CharacterID)
		if inst == nil || inst.definition.Name != content.KentaPQName {
			return false, nil
		}
		go s.eventExit(inst.channel, inst.definition, live, sink)
		return true, nil
	case "kenta_nextStage":
	default:
		return false, nil
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.definition.Name != content.KentaPQName || session.Character.HP <= 0 ||
		s.partySnapshotOf(session.CharacterID).LeaderID != session.CharacterID {
		return true, sink.WritePacket(protocol.PortalTrainingMessage("請讓仍在場內的隊長帶大家前進。"))
	}
	mapID := session.Character.MapID
	switch mapID {
	case content.KentaRescue:
		if s.kentaMonstersRemain(inst.channel, mapID) {
			return true, sink.WritePacket(protocol.PortalTrainingMessage("請先擊退所有威脅坎特的怪物。"))
		}
	case content.KentaBubbles:
		if !s.markKentaBubblesReady(inst) {
			return true, sink.WritePacket(protocol.PortalTrainingMessage("全隊需要收集 20 個氣泡。"))
		}
	default:
		return true, sink.WritePacket(protocol.EnableActions())
	}
	next, err := s.gameplay.catalog.Map(mapID + 100)
	if err != nil {
		return true, err
	}
	entry, ok := next.Spawn()
	if !ok {
		return true, domain.ErrNotFound
	}
	return true, s.moveEventRoom(ctx, inst, mapID, eventOccupant{live, sink}, next.ID, entry)
}

func (s *Server) kentaBubblePicked(session domain.CharacterSession, drop domain.ItemDrop) {
	if session.Character.MapID != content.KentaBubbles || drop.TemplateID != content.KentaBubble {
		return
	}
	if inst := s.eventOf(session.CharacterID); inst != nil && inst.definition.Name == content.KentaPQName {
		s.markKentaBubblesReady(inst)
	}
}

func (s *Server) markKentaBubblesReady(inst *eventInstance) bool {
	s.events.mu.Lock()
	ended, ready := inst.ended, inst.kentaBubblesDone
	s.events.mu.Unlock()
	if ended || ready {
		return !ended
	}
	group := s.eventOccupants(inst.channel, []int{content.KentaBubbles})
	total := 0
	for _, member := range group {
		session := partySession(member.live)
		if s.eventOf(session.CharacterID) == inst {
			total += henesysItemCount(session.Character, content.KentaBubble)
		}
	}
	if total < 20 {
		return false
	}
	s.events.mu.Lock()
	if inst.ended {
		s.events.mu.Unlock()
		return false
	}
	firstClear := !inst.kentaBubblesDone
	inst.kentaBubblesDone = true
	s.events.mu.Unlock()
	if firstClear {
		effect := eventEffects["clear"]
		s.broadcastInventoryPacket(inst.channel, content.KentaBubbles, protocol.FieldScreenEffect(effect[0]))
		s.broadcastInventoryPacket(inst.channel, content.KentaBubbles, protocol.FieldSoundEffect(effect[1]))
	}
	return true
}

func (s *Server) prepareKenta(ctx context.Context, inst *eventInstance, group []eventOccupant) error {
	for _, member := range group {
		err := s.applyEventExchange(ctx, member, "kenta-preparation-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
			if quantity := henesysItemCount(current.Character, content.KentaBubble); quantity > 0 {
				return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: content.KentaBubble, Quantity: -quantity}}}, nil
			}
			return domain.ScriptExchange{}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) kentaMonstersRemain(channel, mapID int) bool {
	game := s.gameplay
	game.mu.Lock()
	defer game.mu.Unlock()
	room := game.rooms[mapKey{channel, mapID}]
	if room == nil {
		return true
	}
	for _, mob := range room.mobs {
		if mob.hp > 0 || mob.claiming || mob.reservation != nil {
			return true
		}
	}
	return false
}

func (s *Server) startKentaDefense(inst *eventInstance, session domain.CharacterSession) bool {
	game := s.gameplay
	game.mu.Lock()
	room := game.rooms[mapKey{inst.channel, content.KentaDefense}]
	if room == nil || room.members[session.CharacterID] == nil || len(room.kentaWaves) == 0 {
		game.mu.Unlock()
		return false
	}
	s.events.mu.Lock()
	if inst.ended || !inst.kentaDefenseAt.IsZero() {
		s.events.mu.Unlock()
		game.mu.Unlock()
		return false
	}
	inst.kentaDefenseAt = time.Now().Add(kentaDefenseTime)
	s.events.mu.Unlock()
	var updates []roomDelivery
	for id, mob := range room.kentaWaves {
		room.mobs[id] = mob
		mob.controller = session.CharacterID
		updates = append(updates, roomBroadcast(room, mob.spawnPacket(false, time.Now()), -1)...)
		updates = append(updates, roomDelivery{room.members[session.CharacterID].sink, mob.spawnPacket(true, time.Now())})
	}
	clear(room.kentaWaves)
	updates = append(updates, roomBroadcast(room, protocol.Clock(int(kentaDefenseTime/time.Second)), -1)...)
	game.mu.Unlock()
	deliver(updates)
	return true
}

func (s *Server) finishKentaDefense(inst *eventInstance) {
	game := s.gameplay
	game.mu.Lock()
	room := game.rooms[mapKey{inst.channel, content.KentaDefense}]
	if room == nil {
		game.mu.Unlock()
		return
	}
	for _, mob := range room.mobs {
		if mob.claiming || mob.reservation != nil {
			game.mu.Unlock()
			return
		}
	}
	var updates []roomDelivery
	for id, mob := range room.mobs {
		if mob.life.TemplateID == content.KentaEscort {
			continue
		}
		delete(room.mobs, id)
		if mob.hp > 0 {
			updates = append(updates, roomBroadcast(room, protocol.KillMonster(id), -1)...)
		}
	}
	game.mu.Unlock()
	deliver(updates)
	s.events.mu.Lock()
	inst.kentaDefended = true
	s.events.mu.Unlock()
	for _, member := range s.eventOccupants(inst.channel, []int{content.KentaDefense}) {
		if s.eventOf(partySession(member.live).CharacterID) == inst {
			_ = member.sink.WritePacket(protocol.Clock(max(0, int(time.Until(inst.deadline)/time.Second))))
		}
	}
}

func (s *Server) kentaFriendlyDamage(live *liveCharacter, sink packetSink, hit protocol.MobHitEscort, now time.Time) {
	session := partySession(live)
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.definition.Name != content.KentaPQName || session.Character.MapID != content.KentaDefense || int64(hit.CharacterID) != session.CharacterID {
		return
	}
	s.events.mu.Lock()
	defending := !inst.ended && !inst.kentaDefended && now.Before(inst.kentaDefenseAt)
	s.events.mu.Unlock()
	if !defending {
		return
	}
	visit := s.sceneFor(live, sink)
	game := s.gameplay
	game.mu.Lock()
	room := game.rooms[mapKey{session.ChannelID, content.KentaDefense}]
	if visit == nil || room == nil || room.members[session.CharacterID] == nil || room.members[session.CharacterID].visit != visit {
		game.mu.Unlock()
		return
	}
	from, to := room.mobs[hit.Attacker], room.mobs[hit.Escort]
	if from == nil || to == nil || from.controller != session.CharacterID || from.hp <= 0 || to.hp <= 0 ||
		to.life.TemplateID != content.KentaEscort || from == to || abs(from.life.X-to.life.X) > 450 ||
		abs(from.life.Y-to.life.Y) > 280 || now.Sub(from.friendlyAttackAt) < 500*time.Millisecond {
		game.mu.Unlock()
		return
	}
	from.friendlyAttackAt = now
	damage := min(to.hp, int64(max(1, from.stats.PAD)))
	to.hp -= damage
	dead := to.hp == 0
	updates := roomBroadcast(room, protocol.FriendlyDamage(to.objectID, int(damage), int(to.hp), int(to.stats.HP)), -1)
	game.mu.Unlock()
	deliver(updates)
	if dead {
		s.eventEnd(inst)
	}
}

func (a *worldScript) finishKenta(ctx context.Context, session domain.CharacterSession) (string, error) {
	live, sink := a.live, a.sink
	inst := a.server.eventOf(session.CharacterID)
	if inst == nil || inst.definition.Name != content.KentaPQName || a.npcID != 9020004 || session.Character.HP <= 0 {
		return "wait", nil
	}
	server := a.server
	switch session.Character.MapID {
	case content.KentaDefense:
		server.events.mu.Lock()
		defended := inst.kentaDefended && !inst.ended
		server.events.mu.Unlock()
		if server.partySnapshotOf(session.CharacterID).LeaderID != session.CharacterID {
			return "defend", nil
		}
		if !defended {
			if server.startKentaDefense(inst, session) {
				return "started", nil
			}
			return "defend", nil
		}
		target, err := server.gameplay.catalog.Map(content.KentaBoss)
		if err != nil {
			return "wait", err
		}
		entry, ok := target.Spawn()
		if !ok {
			return "wait", domain.ErrNotFound
		}
		for _, peer := range server.eventOccupants(inst.channel, []int{content.KentaDefense}) {
			if peer.live == live || server.eventOf(partySession(peer.live).CharacterID) != inst {
				continue
			}
			server.cancelNPCScriptState(peer.live, false)
			server.closeEconomyDialog(peer.live)
			if err := server.warpEventMember(ctx, inst, content.KentaDefense, peer, target.ID, entry); err != nil {
				return "wait", err
			}
		}
		return "moved", server.warpEventMember(ctx, inst, content.KentaDefense, eventOccupant{live, sink}, target.ID, entry)
	case content.KentaBoss:
		if server.kentaMonstersRemain(inst.channel, content.KentaBoss) {
			return "boss", nil
		}
	}
	if session.Character.MapID != content.KentaBoss {
		return "wait", nil
	}
	server.events.mu.Lock()
	if inst.ended {
		server.events.mu.Unlock()
		return "wait", nil
	}
	inst.cleared = true
	server.events.mu.Unlock()
	err := server.applyEventExchange(ctx, eventOccupant{live, sink}, "kenta-clear-"+inst.id,
		func(current domain.CharacterSession) (domain.ScriptExchange, error) {
			if current.Character.MapID != content.KentaBoss || server.eventOf(current.CharacterID) != inst {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			level := current.Character.Level
			scale := level
			if level > 200 {
				scale = 200 + level/10
			}
			divisor := int64(max(1, min(level, 200)) * 3)
			var items []domain.ScriptItemDelta
			if quantity := henesysItemCount(current.Character, content.KentaBubble); quantity > 0 {
				items = append(items, domain.ScriptItemDelta{TemplateID: content.KentaBubble, Quantity: -quantity})
			}
			return domain.ScriptExchange{
				Items: items,
				EXP:   (20*content.ExperienceForLevel(scale) + divisor/2) / divisor,
				Logs:  []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "kenta-pq-clear"}, Delta: 1}},
			}, nil
		})
	if errors.Is(err, domain.ErrInsufficient) {
		return "inventory", nil
	}
	if err != nil {
		return "wait", err
	}
	return "reward", nil
}
