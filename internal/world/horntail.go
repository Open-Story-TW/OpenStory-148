package world

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type horntailStage struct {
	event         string
	intro, first  int
	count, marker int
	next          int
	ground        content.Life
	crystal       content.Reactor
}

func horntailByMap(mapID int) (horntailStage, bool) {
	difficulty := mapID % 100
	if difficulty != 0 && difficulty != 1 {
		return horntailStage{}, false
	}
	stage := horntailStage{event: "Horntail"}
	if difficulty == 1 {
		stage.event = "ChaosHorntail"
	}
	switch mapID - difficulty {
	case 240060000:
		stage.intro, stage.first, stage.count = 8810024, 8810000, 1
		stage.next = 240060100 + difficulty
		stage.ground = content.Life{X: 970, Y: 230, Foothold: 7}
		stage.crystal = content.Reactor{TemplateID: 2408003, X: 970, Y: 225}
	case 240060100:
		stage.intro, stage.first, stage.count = 8810025, 8810001, 1
		stage.next = 240060200 + difficulty
		stage.ground = content.Life{X: -439, Y: 260, Foothold: 17}
		stage.crystal = content.Reactor{TemplateID: 2408003, X: -439, Y: 251}
	case 240060200:
		stage.intro, stage.first, stage.count, stage.marker = 8810026, 8810002, 8, 8810018
		stage.ground = content.Life{X: 71, Y: 260, Foothold: 5}
		stage.crystal = content.Reactor{TemplateID: 2401000, X: 598, Y: -91, Respawn: -1}
	default:
		return horntailStage{}, false
	}
	if difficulty == 1 {
		stage.intro += 104
		stage.first += 100
		if stage.marker != 0 {
			stage.marker += 100
			stage.crystal = content.Reactor{TemplateID: 2401100, X: 655, Y: -85, Respawn: -1}
		}
	}
	return stage, true
}

type horntailField struct {
	stage     horntailStage
	crystal   *roomReactor
	opening   *gameplayMob
	marker    *gameplayMob
	parts     []*gameplayMob
	remains   []content.Mob
	deadParts map[int32]bool
	summoned  bool
	cleared   bool
	sealing   bool
}

type horntailLootRepository interface {
	CompleteHorntailLoot(context.Context, domain.CharacterSession, string, int, int) (string, error)
}

func (g *gameplaySpace) newHorntailMob(stage horntailStage, mob content.Mob, sceneOnly bool) *gameplayMob {
	id := g.alloc()
	if id == 0 {
		return nil
	}
	life := stage.ground
	life.Kind, life.TemplateID, life.RespawnSeconds = "m", mob.ID, -1
	return &gameplayMob{objectID: id, life: life, origin: life, stats: mob, hp: mob.HP, fake: sceneOnly}
}

func (g *gameplaySpace) prepareHorntail(room *gameplayRoom, stage horntailStage) error {
	get := func(id int, sceneOnly bool) (*gameplayMob, error) {
		stats, err := g.catalog.Mob(id)
		if err != nil {
			return nil, err
		}
		m := g.newHorntailMob(stage, stats, sceneOnly)
		if m == nil {
			return nil, errors.New("unable to allocate a Horntail object")
		}
		return m, nil
	}
	opening, err := get(stage.intro, true)
	if err != nil {
		return err
	}
	x := &roomReactor{objectID: g.alloc(), def: stage.crystal}
	if x.objectID == 0 {
		return errors.New("unable to allocate Horntail crystal")
	}
	if room.reactors == nil {
		room.reactors = map[int32]*roomReactor{}
	}
	room.reactors[x.objectID] = x
	field := &horntailField{stage: stage, crystal: x, opening: opening, deadParts: map[int32]bool{}}
	for i := range stage.count {
		m, err := get(stage.first+i, false)
		if err != nil {
			return err
		}
		field.parts = append(field.parts, m)
		if stage.count == 8 {
			stats, err := g.catalog.Mob(8810010 + i)
			if err != nil {
				return err
			}
			field.remains = append(field.remains, stats)
		}
	}
	if stage.marker != 0 {
		field.marker, err = get(stage.marker, true)
		if err != nil {
			return err
		}
		_, reward, _ := content.HorntailLoot(room.definition.ID)
		if _, err := g.catalog.Mob(reward); err != nil {
			return err
		}
	}
	room.horntail = field
	return nil
}

func (s *Server) horntailStagePortal(ctx context.Context, actor domain.CharacterSession, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	stage, ok := horntailByMap(actor.Character.MapID)
	if !ok || stage.next == 0 {
		return false, nil
	}
	key := mapKey{channel: actor.ChannelID, mapID: actor.Character.MapID}
	switch portal.Script {
	case "hontale_boss1", "hontale_boss2":
		want := "hontale_boss1"
		if stage.first%100 == 1 {
			want = "hontale_boss2"
		}
		if portal.Name == "mob00" && portal.Script == want {
			s.beginHorntailStage(key, actor.CharacterID)
			return true, sink.WritePacket(protocol.EnableActions())
		}
	case "hontale_BR":
		if portal.Name != "next00" {
			break
		}
		s.gameplay.mu.Lock()
		room := s.gameplay.rooms[key]
		clear := room != nil && room.horntail != nil && room.horntail.cleared
		s.gameplay.mu.Unlock()
		if !clear {
			_ = sink.WritePacket(protocol.PortalTrainingMessage("請先擊敗這裡的頭顱，入口才能開啟。"))
			return true, sink.WritePacket(protocol.EnableActions())
		}
		return true, s.advanceExpedition(ctx, actor, live, sink, stage.event, stage.next)
	}
	return false, nil
}

func (s *Server) hitHorntailCrystal(channel int, actor domain.Character, objectID int32) bool {
	stage, ok := horntailByMap(actor.MapID)
	if !ok || stage.next != 0 || actor.HP <= 0 {
		return false
	}
	key := mapKey{channel: channel, mapID: actor.MapID}
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.horntail == nil || room.horntail.crystal.objectID != objectID {
		g.mu.Unlock()
		return false
	}
	field := room.horntail
	if field.summoned || abs(actor.X-stage.crystal.X) > 155 || abs(actor.Y-stage.crystal.Y) > 155 ||
		!s.expeditionCombatAllowedLocked(key, actor.ID) {
		g.mu.Unlock()
		return true
	}
	// Both v148 crystal reactors move through states 0 to 6 on normal hits.
	if field.crystal.state < 5 {
		field.crystal.state++
		state := field.crystal.state
		g.mu.Unlock()
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.ReactorState(objectID, state, stage.crystal.X, stage.crystal.Y))
		return true
	}
	g.mu.Unlock()
	s.beginHorntailStage(key, actor.ID)
	return true
}

func (s *Server) beginHorntailStage(key mapKey, actor int64) {
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.horntail == nil || room.horntail.summoned || !s.expeditionCombatAllowedLocked(key, actor) {
		g.mu.Unlock()
		return
	}
	field := room.horntail
	field.summoned = true
	field.crystal.state = 1
	if field.stage.next == 0 {
		field.crystal.state = 6
	}
	state := field.crystal.state
	room.mobs[field.opening.objectID] = field.opening
	g.mu.Unlock()
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.ReactorState(field.crystal.objectID, state, field.crystal.def.X, field.crystal.def.Y))
	if field.stage.next == 0 {
		s.broadcastInventoryPacket(key.channel, key.mapID, protocol.FieldMusic("Bgm14/HonTale"))
	}
	s.broadcastInventoryPacket(key.channel, key.mapID, field.opening.spawnPacket(false, time.Now()))
	time.AfterFunc(time.Second, func() {
		g.mu.Lock()
		if g.rooms[key] != room || !s.horntailAnyMemberReadyLocked(key, room) {
			g.mu.Unlock()
			return
		}
		delete(room.mobs, field.opening.objectID)
		packets := [][]byte{protocol.RemoveMonster(field.opening.objectID)}
		for _, m := range field.parts {
			room.mobs[m.objectID] = m
			packets = append(packets, m.spawnPacket(false, time.Now()))
		}
		if m := field.marker; m != nil {
			room.mobs[m.objectID] = m
			packets = append(packets, m.spawnPacket(false, time.Now()))
		}
		g.mu.Unlock()
		for _, p := range packets {
			s.broadcastInventoryPacket(key.channel, key.mapID, p)
		}
	})
}

func (s *Server) horntailAnyMemberReadyLocked(key mapKey, room *gameplayRoom) bool {
	for id := range room.members {
		if s.expeditionCombatAllowedLocked(key, id) {
			return true
		}
	}
	return false
}

func (s *Server) horntailDefeated(ctx context.Context, ticket *combatTicket, target domain.CombatTarget) {
	key, actor := ticket.key, ticket.actorSession.CharacterID
	g := s.gameplay
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.horntail == nil || !s.expeditionCombatAllowedLocked(key, actor) {
		g.mu.Unlock()
		return
	}
	field := room.horntail
	var stagePackets [][]byte
	credited := false
	for i, m := range field.parts {
		if m.objectID != target.ObjectID || field.deadParts[m.objectID] {
			continue
		}
		field.deadParts[m.objectID] = true
		credited = true
		if field.stage.count == 8 {
			if corpse := g.newHorntailMob(field.stage, field.remains[i], true); corpse != nil {
				room.mobs[corpse.objectID] = corpse
				stagePackets = append(stagePackets, corpse.spawnPacket(false, time.Now()))
			}
		}
	}
	won := credited && len(field.deadParts) == len(field.parts) && !field.cleared && !field.sealing
	seal := won && field.stage.next == 0
	if !won {
		g.mu.Unlock()
	} else if field.stage.next != 0 {
		field.cleared = true
		g.mu.Unlock()
		stagePackets = append(stagePackets, protocol.PortalTrainingMessage("已擊敗頭顱，可以進入下一個洞穴。"))
	} else {
		field.sealing = true
		g.mu.Unlock()
	}
	for _, p := range stagePackets {
		s.broadcastInventoryPacket(key.channel, key.mapID, p)
	}
	if seal {
		s.finishHorntail(ctx, room, ticket, target, 3)
	}
}

func (s *Server) finishHorntail(ctx context.Context, room *gameplayRoom, ticket *combatTicket, last domain.CombatTarget, retries int) {
	repo, ok := s.repo.(horntailLootRepository)
	if !ok {
		return
	}
	current := partySession(ticket.actor)
	current.Lease = s.currentLease()
	step, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	op, err := repo.CompleteHorntailLoot(step, current, last.SpawnOperationID, room.horntail.stage.ground.X, room.horntail.stage.ground.Y)
	key := ticket.key
	g := s.gameplay
	g.mu.Lock()
	field := room.horntail
	if g.rooms[key] != room {
		g.mu.Unlock()
		return
	}
	if err != nil {
		retry := retries > 0 && !combatKnownFailure(err) && s.horntailAnyMemberReadyLocked(key, room)
		field.sealing = retry
		g.mu.Unlock()
		if !retry {
			slog.Warn("horntail_loot_failed", "map", key.mapID, "error", err)
			return
		}
		time.AfterFunc(time.Second, func() {
			g.mu.Lock()
			currentRoom := g.rooms[key] == room && s.horntailAnyMemberReadyLocked(key, room)
			g.mu.Unlock()
			if currentRoom {
				s.finishHorntail(ctx, room, ticket, last, retries-1)
			}
		})
		return
	}
	field.sealing = false
	field.cleared = true
	var packets [][]byte
	for oid, mob := range room.mobs {
		if mob.fake {
			delete(room.mobs, oid)
			packets = append(packets, protocol.RemoveMonster(oid))
		}
	}
	s.events.mu.Lock()
	if inst := s.events.byMember[current.CharacterID]; inst != nil && !inst.ended {
		inst.cleared = true
	}
	s.events.mu.Unlock()
	g.mu.Unlock()
	_ = s.spawnCombatLoot(ctx, ticket.actor, ticket.sink, op, 0, 0, 0)
	for _, p := range packets {
		s.broadcastInventoryPacket(key.channel, key.mapID, p)
	}
	s.broadcastInventoryPacket(key.channel, key.mapID, protocol.PortalTrainingMessage("已擊敗闇黑龍王！請拾取獎勵，再從水晶離開。"))
}

func hasHorntailDisguise(actor domain.Character, now time.Time) bool {
	for _, buff := range actor.SkillBuffs {
		if buff.ID == -content.HorntailDisguiseItem && buff.Stat == "MORPH" && buff.Value == 4 && buff.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

func (a *worldScript) openHorntailCavern(ctx context.Context, actor domain.CharacterSession) (bool, error) {
	if a.npcID != 2081005 || a.objectID == 0 {
		return false, nil
	}
	return a.server.enterHorntailCavern(ctx, actor, a.live, a.sink)
}

func (s *Server) enterHorntailCavern(ctx context.Context, actor domain.CharacterSession, live *liveCharacter, sink packetSink) (bool, error) {
	if actor.Character.MapID != 240040700 || actor.Character.HP <= 0 ||
		!hasHorntailDisguise(actor.Character, time.Now()) {
		return false, nil
	}
	dest, err := s.gameplay.catalog.Map(240050000)
	if err != nil {
		return false, err
	}
	spawn, ok := dest.Spawn()
	if !ok {
		return false, domain.ErrNotFound
	}
	if err := s.warpGameplay(ctx, actor.ChannelID, live, sink, dest.ID, spawn); err != nil {
		return false, err
	}
	return true, s.cancelSkill(ctx, live, sink, -content.HorntailDisguiseItem)
}
