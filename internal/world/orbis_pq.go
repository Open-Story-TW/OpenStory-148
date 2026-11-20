package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const orbisCloudCount = 20

type orbisRun struct {
	ascent       [8]int
	restingLever int
	bonusStarted bool
}

func newOrbisRun() *orbisRun {
	run := &orbisRun{restingLever: rand.IntN(3)}
	for i := range run.ascent {
		run.ascent[i] = 1 + rand.IntN(4)
	}
	return run
}

func (s *Server) prepareOrbisPQ(ctx context.Context, inst *eventInstance, occupants []eventOccupant) error {
	repo, ok := s.repo.(domain.EventDropRepository)
	if !ok {
		return domain.ErrForbidden
	}
	for _, mapID := range inst.definition.Maps {
		if err := repo.ExpireEventDrops(ctx, s.cfg.WorldID, inst.channel, mapID); err != nil {
			return err
		}
	}
	for _, member := range occupants {
		err := s.applyEventExchange(ctx, member, "orbis-preparation-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
			var items []domain.ScriptItemDelta
			for id := 4001044; id <= content.OrbisCloudPiece; id++ {
				if n := henesysItemCount(current.Character, id); n > 0 {
					items = append(items, domain.ScriptItemDelta{TemplateID: id, Quantity: -n})
				}
			}
			return domain.ScriptExchange{Items: items}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) hitOrbisCloud(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	session := partySession(live)
	if session.Character.MapID != content.OrbisPQEntrance {
		return false, nil
	}
	inst := s.eventOf(session.CharacterID)
	visit := s.sceneFor(live, sink)
	if inst == nil || inst.definition.Name != content.OrbisPQName || visit == nil || session.Character.HP <= 0 || s.tradeActive(live) {
		return true, nil
	}
	g := s.gameplay
	key := mapKey{channel: channel, mapID: content.OrbisPQEntrance}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[session.CharacterID] == nil || room.members[session.CharacterID].visit != visit {
		g.mu.Unlock()
		return true, nil
	}
	cloud := room.reactors[oid]
	if cloud == nil || cloud.def.TemplateID != content.OrbisCloudReactor || cloud.state >= 4 || cloud.finishing ||
		!within(session.Character.X, session.Character.Y, cloud.def.X, cloud.def.Y, 148) || time.Since(cloud.lastHit) < 230*time.Millisecond {
		g.mu.Unlock()
		return true, nil
	}
	cloud.lastHit = time.Now()
	if cloud.state < 3 {
		cloud.state++
		updates := roomBroadcast(room, protocol.ReactorState(oid, cloud.state, cloud.def.X, cloud.def.Y), -1)
		g.mu.Unlock()
		deliver(updates)
		return true, nil
	}
	cloud.finishing = true
	def := cloud.def
	ground, ok := groundBelow(room.definition, def.X, def.Y)
	if !ok {
		ground = def.Y
	}
	g.mu.Unlock()

	op := fmt.Sprintf("orbis-%s-cloud-%d", inst.id, oid)
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, op, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.ChannelID != channel || current.Character.MapID != key.mapID || current.Character.HP <= 0 ||
			s.eventOf(current.CharacterID) != inst || s.sceneFor(live, sink) != visit {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		return domain.ScriptExchange{Drops: []domain.ScriptDrop{{TemplateID: content.OrbisCloudPiece, Quantity: 1, X: def.X, Y: ground}}}, nil
	})
	g.mu.Lock()
	cloud.finishing = false
	if err != nil || g.rooms[key] != room {
		g.mu.Unlock()
		return true, err
	}
	cloud.state = 4
	updates := roomBroadcast(room, protocol.ReactorState(oid, 4, def.X, def.Y), -1)
	if seal := orbisEak(room); seal != nil && seal.state < orbisCloudCount {
		seal.state++
		updates = append(updates, roomBroadcast(room, protocol.ReactorState(seal.objectID, seal.state, seal.def.X, seal.def.Y), -1)...)
	}
	g.mu.Unlock()
	deliver(updates)
	return true, nil
}

func orbisEak(room *gameplayRoom) *roomReactor {
	if room != nil {
		for _, reactor := range room.reactors {
			if reactor.def.TemplateID == content.OrbisEakReactor {
				return reactor
			}
		}
	}
	return nil
}

func (s *Server) offerOrbisClouds(ctx context.Context, session domain.CharacterSession, drop domain.ItemDrop) {
	if drop.MapID != content.OrbisPQEntrance || session.Character.MapID != drop.MapID || drop.ChannelID != session.ChannelID ||
		drop.OwnerID != session.CharacterID || !drop.PlayerDrop || drop.TemplateID != content.OrbisCloudPiece || drop.Quantity != orbisCloudCount {
		return
	}
	inst := s.eventOf(session.CharacterID)
	writer, ok := s.repo.(domain.EventDropRepository)
	if inst == nil || inst.definition.Name != content.OrbisPQName || !ok {
		return
	}
	g := s.gameplay
	key := mapKey{channel: session.ChannelID, mapID: drop.MapID}
	g.mu.Lock()
	room := g.rooms[key]
	seal := orbisEak(room)
	if seal == nil || seal.finishing || seal.state != orbisCloudCount ||
		drop.X < seal.def.X-33 || drop.X > seal.def.X+34 || drop.Y < seal.def.Y-36 || drop.Y > seal.def.Y+81 {
		g.mu.Unlock()
		return
	}
	oid := g.alloc()
	if oid == 0 {
		g.mu.Unlock()
		return
	}
	seal.finishing = true
	g.mu.Unlock()

	consumed, err := writer.ConsumeEventDrop(ctx, drop)
	g.mu.Lock()
	seal.finishing = false
	if err != nil || !consumed || g.rooms[key] != room {
		g.mu.Unlock()
		if consumed {
			s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
		}
		return
	}
	seal.state = orbisCloudCount + 1
	updates := roomBroadcast(room, protocol.ReactorState(seal.objectID, seal.state, seal.def.X, seal.def.Y), -1)
	floor, found := groundBelow(room.definition, seal.def.X, seal.def.Y)
	if !found {
		floor = seal.def.Y
	}
	npc := content.Life{Kind: "n", TemplateID: 2013001, X: seal.def.X, Y: floor, RX0: seal.def.X - 40, RX1: seal.def.X + 40}
	room.npcs[oid] = npc
	for _, packet := range s.npcScriptability(map[int32]content.Life{oid: npc}) {
		updates = append(updates, roomBroadcast(room, packet, -1)...)
	}
	updates = append(updates, roomBroadcast(room, protocol.SpawnNPC(oid, npc, false), -1)...)
	updates = append(updates, roomBroadcast(room, protocol.SpawnNPC(oid, npc, true), -1)...)
	g.mu.Unlock()
	s.removeVisibleDrop(drop, protocol.RemoveItemExpired(drop.ObjectID))
	deliver(updates)
}

func (s *Server) orbisEntrancePortal(ctx context.Context, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	session := partySession(live)
	if session.Character.MapID != content.OrbisPQEntrance || p.Name != "in00" || p.TargetMap != content.OrbisPQHall {
		return false, nil
	}
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.definition.Name != content.OrbisPQName || s.partySnapshotOf(session.CharacterID).LeaderID != session.CharacterID {
		return true, sink.WritePacket(protocol.PortalTrainingMessage("請讓隊長確認封印。"))
	}
	g := s.gameplay
	g.mu.Lock()
	seal := orbisEak(g.rooms[mapKey{channel: session.ChannelID, mapID: content.OrbisPQEntrance}])
	open := seal != nil && seal.state == orbisCloudCount+1
	g.mu.Unlock()
	if !open {
		return true, sink.WritePacket(protocol.PortalTrainingMessage("將 20 個雲朵碎片放在封印前，救出侍從。"))
	}
	destination, err := g.catalog.Map(content.OrbisPQHall)
	if err != nil {
		return true, err
	}
	entry, ok := destination.Portal(p.TargetName)
	if !ok {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	if err = s.awardEventStageEXP(ctx, inst, content.OrbisPQEntrance, 6000); err != nil {
		return true, err
	}
	return true, s.moveEventRoom(ctx, inst, content.OrbisPQEntrance, eventOccupant{live, sink}, destination.ID, entry)
}

func (s *Server) orbisRoomPortal(ctx context.Context, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	if !strings.HasPrefix(p.Script, "party3_") {
		return false, nil
	}
	session := partySession(live)
	inst := s.eventOf(session.CharacterID)
	if inst == nil || inst.orbis == nil {
		return false, nil
	}
	mapID := session.Character.MapID
	if p.Script == "party3_r6pt" && mapID == content.OrbisPQAscent {
		return true, s.orbisAscentPortal(ctx, inst, live, sink, p)
	}
	target := 0
	switch {
	case mapID == content.OrbisPQHall:
		switch p.Script {
		case "party3_room1":
			target = content.OrbisPQWalkway
		case "party3_room2":
			target = content.OrbisPQStorage
		case "party3_room3":
			target = content.OrbisPQMusic
		case "party3_room6":
			target = content.OrbisPQAscent
		case "party3_room8":
			target = content.OrbisPQHidden
		}
	case mapID == content.OrbisPQGarden && p.Script == "party3_gardenin":
		if henesysItemCount(session.Character, 4001055) < 1 {
			return true, sink.WritePacket(protocol.PortalTrainingMessage("隊長需要生命之根才能帶大家返回女神像。"))
		}
		target = content.OrbisPQHall
	case p.Script == "party3_roomout" && (mapID == content.OrbisPQWalkway || mapID == content.OrbisPQStorage ||
		mapID == content.OrbisPQMusic || mapID == content.OrbisPQAscent || mapID == content.OrbisPQHidden):
		target = content.OrbisPQHall
	}
	if target == 0 {
		return false, nil
	}
	if s.partySnapshotOf(session.CharacterID).LeaderID != session.CharacterID {
		return true, sink.WritePacket(protocol.PortalTrainingMessage("請讓隊長帶大家通過傳點。"))
	}
	m, err := s.gameplay.catalog.Map(target)
	if err != nil {
		return true, err
	}
	entry, ok := m.Spawn()
	if !ok {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	return true, s.moveEventRoom(ctx, inst, mapID, eventOccupant{live, sink}, target, entry)
}

func (s *Server) orbisAscentPortal(ctx context.Context, inst *eventInstance, live *liveCharacter, sink packetSink, p content.Portal) error {
	if len(p.Name) != 5 || p.Name[:2] != "rp" {
		return sink.WritePacket(protocol.EnableActions())
	}
	row, rowErr := strconv.Atoi(p.Name[2:4])
	choice, choiceErr := strconv.Atoi(p.Name[4:])
	if rowErr != nil || choiceErr != nil || row < 1 || row > len(inst.orbis.ascent) || choice < 1 || choice > 4 {
		return sink.WritePacket(protocol.EnableActions())
	}
	correct := inst.orbis.ascent[row-1] == choice
	destination := p.ID - 4
	label := "這個踏板通往下方。"
	if correct {
		destination = p.ID + 4
		label = "走對踏板了。"
		if row == 8 {
			destination = 2
		}
	} else if row == 1 {
		destination = 5
	} else if row == 5 {
		destination = 1
	}
	m, err := s.gameplay.catalog.Map(content.OrbisPQAscent)
	if err != nil {
		return err
	}
	for _, entry := range m.Portals {
		if entry.ID == destination {
			guard := func(current domain.CharacterSession) bool {
				return current.Character.MapID == m.ID && current.Character.HP > 0 && s.eventOf(current.CharacterID) == inst
			}
			return s.warpGameplayConditional(ctx, inst.channel, live, sink, m.ID, entry, nil, guard,
				[][]byte{protocol.PortalTrainingMessage(label)})
		}
	}
	return sink.WritePacket(protocol.EnableActions())
}
