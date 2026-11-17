package world

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const treasureMapItem = 4031018

func treasureFields() []int {
	maps := []int{content.TreasureHuntLobby}
	for id := 109010100; id <= 109010110; id++ {
		maps = append(maps, id)
	}
	for id := 109010200; id <= 109010206; id++ {
		maps = append(maps, id)
	}
	return maps
}

func (s *Server) treasureRun(session domain.CharacterSession) *treasureHuntRun {
	if s.quizzes == nil || !content.TreasureHuntMap(session.Character.MapID) || session.Character.HP <= 0 {
		return nil
	}
	s.quizzes.mu.Lock()
	defer s.quizzes.mu.Unlock()
	run := s.quizzes.hunts[session.ChannelID]
	if run == nil || run.phase.Load() != treasureHuntRunning || !time.Now().Before(run.deadline) ||
		run.members[session.CharacterID] != session.Character.MapID || !run.course[session.CharacterID] {
		return nil
	}
	return run
}

func (s *Server) hitTreasureChest(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	session := partySession(live)
	mapID := session.Character.MapID
	if !content.TreasureHuntMap(mapID) {
		return false, nil
	}
	visit := s.sceneFor(live, sink)
	run := s.treasureRun(session)
	if visit == nil || run == nil || s.tradeActive(live) {
		return true, nil
	}
	g := s.gameplay
	key := mapKey{channel, mapID}
	g.mu.Lock()
	room := g.rooms[key]
	if room == nil || room.members[session.CharacterID] == nil || room.members[session.CharacterID].visit != visit {
		g.mu.Unlock()
		return true, nil
	}
	x := room.reactors[oid]
	if x == nil || x.gone || x.finishing || !content.TreasureHuntReactor(mapID, x.def.TemplateID) ||
		abs(x.def.X-session.Character.X) > 140 || abs(x.def.Y-session.Character.Y) > 90 || time.Since(x.lastHit) < 200*time.Millisecond {
		g.mu.Unlock()
		return true, nil
	}
	x.lastHit = time.Now()
	x.state++
	def := x.def
	var visits []*sceneVisit
	for _, member := range room.members {
		visits = append(visits, member.visit)
	}
	if x.state < gatherLastState {
		packet := protocol.ReactorState(oid, x.state, def.X, def.Y)
		g.mu.Unlock()
		for _, observer := range visits {
			_ = s.writeScenePackets(observer, packet)
		}
		return true, nil
	}
	x.finishing = true
	op := fmt.Sprintf("treasure-%s-%d-%d", run.id, oid, x.growth)
	g.mu.Unlock()

	var err error
	if def.TemplateID == 9001000 {
		err = s.spawnTreasureSnake(key, def)
	} else {
		err = s.dropTreasureChest(ctx, live, sink, visit, run, op, def)
	}
	g.mu.Lock()
	x.finishing = false
	if err != nil {
		x.state--
		g.mu.Unlock()
		slog.Warn("treasure_chest_failed", "error", err)
		return true, sink.WritePacket(protocol.PortalTrainingMessage("寶箱暫時無法打開，請再試一次。"))
	}
	x.gone = true
	if def.Respawn > 0 {
		x.respawnAt = time.Now().Add(time.Duration(def.Respawn) * time.Second)
	} else {
		x.respawnAt = time.Time{}
	}
	g.mu.Unlock()
	for _, observer := range visits {
		_ = s.writeScenePackets(observer, protocol.ReactorState(oid, gatherLastState, def.X, def.Y), protocol.ReactorLeave(oid, gatherLastState, def.X, def.Y))
	}
	return true, nil
}

func (s *Server) dropTreasureChest(ctx context.Context, live *liveCharacter, sink packetSink, visit *sceneVisit, run *treasureHuntRun, op string, def content.Reactor) error {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return domain.ErrForbidden
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.skillSession(live)
	if !s.sceneCurrent(visit) || s.treasureRun(session) != run || s.tradeActive(live) {
		return domain.ErrStaleOwner
	}
	put := func(template, side int) domain.ScriptDrop {
		x, y := s.gatherDropPoint(session.Character.MapID, def.X, def.Y, side)
		return domain.ScriptDrop{TemplateID: template, Quantity: 1, X: x, Y: y}
	}
	drops := []domain.ScriptDrop{put(2000000, -25)}
	switch def.TemplateID {
	case 9002001:
		drops = append(drops, put(1032000, 25))
	case 9002002:
		drops = append(drops, put(treasureMapItem, 25))
	}
	result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: session, OperationID: op, Drops: drops})
	if err != nil {
		return err
	}
	s.applyInventoryResult(live, result.Character)
	for _, drop := range result.Drops {
		s.broadcastCombatDrop(drop)
	}
	return nil
}

func (s *Server) spawnTreasureSnake(key mapKey, def content.Reactor) error {
	g := s.gameplay
	const snake = 9100001
	stats, err := g.catalog.Mob(snake)
	if err != nil {
		return err
	}
	field, err := g.catalog.Map(key.mapID)
	if err != nil {
		return err
	}
	x, y := s.gatherDropPoint(key.mapID, def.X, def.Y, 20)
	var foothold int
	for _, f := range field.Footholds {
		if f.X1 <= x && f.X2 >= x && f.X2 > f.X1 && abs(f.Y1+(f.Y2-f.Y1)*(x-f.X1)/(f.X2-f.X1)-y) <= 12 {
			foothold = f.ID
			break
		}
	}
	if foothold == 0 {
		return domain.ErrNotFound
	}
	g.mu.Lock()
	room := g.rooms[key]
	oid := g.alloc()
	if oid == 0 || room == nil {
		g.mu.Unlock()
		return domain.ErrNotFound
	}
	life := content.Life{Kind: "m", TemplateID: snake, X: x, Y: y, Foothold: foothold, RX0: x - 100, RX1: x + 100, RespawnSeconds: -1}
	mob := &gameplayMob{objectID: oid, life: life, origin: life, stats: stats, hp: stats.HP}
	room.mobs[oid] = mob
	var ordinary []*sceneVisit
	var controller *sceneVisit
	for id, member := range room.members {
		ordinary = append(ordinary, member.visit)
		if controller == nil {
			controller = member.visit
			mob.controller = id
		}
	}
	g.mu.Unlock()
	for _, observer := range ordinary {
		_ = s.writeScenePackets(observer, protocol.SpawnMonster(oid, life, false))
	}
	if controller != nil {
		_ = s.writeScenePackets(controller, protocol.SpawnMonster(oid, life, true))
	}
	return nil
}
