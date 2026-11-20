package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

const obstaclePrizeMap = 109050000

type obstacleEvent struct {
	name string
	maps []int
	time time.Duration
}

var obstacleEvents = map[string]obstacleEvent{
	"fitness": {name: "楓之谷體能測驗", maps: []int{109040000, 109040001, 109040002, 109040003, 109040004}, time: 10 * time.Minute},
	"ola":     {name: "向上攀升", maps: []int{109030001, 109030002, 109030003}, time: 6 * time.Minute},
}

type obstacleRun struct {
	id       string
	event    obstacleEvent
	fields   []content.Map
	members  map[int64]int // expected field; zero while a member is joining
	course   map[int64]bool
	deadline time.Time
	phase    atomic.Int32 // registration, race and exit
	correct  [3]int
}

const (
	obstacleRegistration = iota
	obstacleRacing
	obstacleClosed
)

func (s *Server) manageObstacle(channel int, name string, action int64, now time.Time) string {
	space := s.quizzes
	if space == nil || s.gameplay == nil {
		return "活動目前無法使用。"
	}
	space.mu.Lock()
	defer space.mu.Unlock()
	run := space.obstacles[channel]
	switch action {
	case 1:
		def, ok := obstacleEvents[name]
		if !ok || run != nil || space.channels[channel] != nil || space.hunts[channel] != nil || space.snowballs[channel] != nil || space.coconuts[channel] != nil {
			return "本頻道已有活動或活動名稱不正確。"
		}
		var fields []content.Map
		for _, id := range def.maps {
			field, err := s.gameplay.catalog.Map(id)
			if err != nil {
				return "活動地圖尚未準備完成。"
			}
			fields = append(fields, field)
		}
		if _, ok := fields[0].Portal("join00"); !ok {
			return "找不到活動入口。"
		}
		if _, err := s.gameplay.catalog.Map(obstaclePrizeMap); err != nil {
			return "找不到活動獎勵地圖。"
		}
		run = &obstacleRun{id: platform.NewID(), event: def, fields: fields, members: make(map[int64]int), course: make(map[int64]bool), deadline: now.Add(5 * time.Minute)}
		if name == "ola" {
			run.correct = [3]int{rand.IntN(5), rand.IntN(8), rand.IntN(15)}
			if run.correct[0] == 2 {
				run.correct[0] = 3
			}
		}
		space.obstacles[channel] = run
		go s.broadcastNotice(0, 0, fmt.Sprintf("頻道 %d 的%s開始報名，換到該頻道並輸入 @event 參加。", channel, def.name))
		return def.name + "已開放報名。"
	case 2:
		if run == nil || run.phase.Load() != obstacleRegistration || !now.Before(run.deadline) {
			return "目前沒有可開始的活動。"
		}
		count := 0
		for _, o := range s.eventOccupants(channel, run.event.maps[:1]) {
			c := partySession(o.live).Character
			if run.members[c.ID] == c.MapID && c.HP > 0 && c.GM == 0 {
				count++
			}
		}
		if count < 2 {
			return "至少需要兩位玩家入場。"
		}
		run.deadline = now.Add(run.event.time)
		run.phase.Store(obstacleRacing)
		go s.broadcastInventoryPacket(channel, run.event.maps[0], protocol.Clock(int(run.event.time.Seconds())))
		return run.event.name + "已開始。"
	case 3:
		if run == nil || run.phase.Swap(obstacleClosed) == obstacleClosed {
			return "目前沒有可取消的活動。"
		}
		go s.endObstacle(channel, run)
		return run.event.name + "已取消。"
	}
	return "無效的活動指令。"
}

func obstacleField(mapID int) bool {
	for _, event := range obstacleEvents {
		if slices.Contains(event.maps, mapID) {
			return true
		}
	}
	return false
}

func (s *Server) obstacleAllowsEntry(session domain.CharacterSession, mapID int) bool {
	if !obstacleField(mapID) {
		return true
	}
	if s.quizzes == nil {
		return false
	}
	s.quizzes.mu.Lock()
	defer s.quizzes.mu.Unlock()
	run := s.quizzes.obstacles[session.ChannelID]
	if run == nil || run.phase.Load() == obstacleClosed || !slices.Contains(run.event.maps, mapID) {
		return false
	}
	field, ok := run.members[session.CharacterID]
	return ok && (field == mapID || field == 0 && mapID == run.event.maps[0])
}

func (s *Server) obstacleEntered(channel int, live *liveCharacter, sink packetSink) {
	session := partySession(live)
	if !obstacleField(session.Character.MapID) || s.quizzes == nil {
		return
	}
	if !s.obstacleAllowsEntry(session, session.Character.MapID) {
		go s.exitObstacle(channel, eventOccupant{live, sink})
		return
	}
	s.quizzes.mu.Lock()
	run := s.quizzes.obstacles[channel]
	left := 0
	if run != nil && run.phase.Load() == obstacleRacing {
		left = int(time.Until(run.deadline).Seconds())
	}
	s.quizzes.mu.Unlock()
	if left > 0 {
		// The map-entry caller still owns the publication gate, as for the
		// party-quest clock. Do not acquire that gate a second time.
		_ = sink.WritePacket(protocol.Clock(left))
	}
}

func (s *Server) obstaclePortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	session := partySession(live)
	from := session.Character.MapID
	if !obstacleField(from) {
		return false, nil
	}
	deny := func() (bool, error) { return true, sink.WritePacket(protocol.EnableActions()) }
	if s.quizzes == nil || s.tradeActive(live) {
		return deny()
	}
	space := s.quizzes
	space.mu.Lock()
	run := space.obstacles[channel]
	if run == nil || run.phase.Load() != obstacleRacing || run.members[session.CharacterID] != from || !time.Now().Before(run.deadline) {
		space.mu.Unlock()
		return deny()
	}
	index := slices.Index(run.event.maps, from)
	mapID, portalName := 0, ""
	if index == 0 && p.Name == "join00" {
		mapID, portalName = from, p.TargetName
	} else if run.course[session.CharacterID] && p.Script == "rand_ola" && from >= 109030001 && from <= 109030003 {
		mapID, portalName = from, "start00"
		if p.Name == fmt.Sprintf("ch%02d", run.correct[index]) {
			mapID = obstaclePrizeMap
			if index+1 < len(run.fields) {
				mapID = run.fields[index+1].ID
			}
		}
	} else if run.course[session.CharacterID] && p.Name == "in00" && from >= 109040000 && from <= 109040004 {
		mapID, portalName = p.TargetMap, p.TargetName
	} else if p.TargetMap == from && p.Type == 1 && p.TargetName != "" {
		mapID, portalName = from, p.TargetName
	} else {
		space.mu.Unlock()
		return deny()
	}
	if mapID == obstaclePrizeMap {
		space.mu.Unlock()
		return true, s.finishObstacle(ctx, channel, live, sink, run)
	}
	to := slices.Index(run.event.maps, mapID)
	if to < 0 || (to != index && to != index+1) {
		space.mu.Unlock()
		return deny()
	}
	entry, ok := run.fields[to].Portal(portalName)
	if !ok {
		space.mu.Unlock()
		return deny()
	}
	id := session.CharacterID
	run.members[id] = mapID
	space.mu.Unlock()
	guard := func(c domain.CharacterSession) bool {
		return c.Character.MapID == from && c.Character.HP > 0 && run.phase.Load() == obstacleRacing
	}
	err := s.warpGameplayConditional(ctx, channel, live, sink, mapID, entry, nil, guard, nil)
	space.mu.Lock()
	if partySession(live).Character.MapID != mapID {
		run.members[id] = from
	} else if p.Name == "join00" && err == nil {
		run.course[id] = true
	}
	space.mu.Unlock()
	return true, err
}

func (s *Server) finishObstacle(ctx context.Context, channel int, live *liveCharacter, sink packetSink, run *obstacleRun) error {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return domain.ErrForbidden
	}
	field, err := s.gameplay.catalog.Map(obstaclePrizeMap)
	if err != nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	entry, ok := field.Portal("start00")
	if !ok {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	session := s.skillSession(live)
	from := session.Character.MapID
	visit := s.sceneFor(live, sink)
	if visit == nil || from != run.event.maps[len(run.event.maps)-1] || run.phase.Load() != obstacleRacing {
		live.persistMu.Unlock()
		return sink.WritePacket(protocol.EnableActions())
	}
	// The legacy reward pool is still being ported. Use the same amount as
	// the OX prize while verifying course progression against the client.
	result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: session, OperationID: "obstacle-" + run.id + "-clear", MesosDelta: 1000000})
	if err == nil {
		s.applyProgressResult(live, result.Character)
	}
	live.persistMu.Unlock()
	if err != nil {
		return sink.WritePacket(protocol.PortalTrainingMessage("獎勵暫時無法領取，請再試一次。"))
	}
	if !result.Replayed {
		_ = s.writeScenePackets(visit, protocol.UpdateMesos(result.Character.Mesos, true), protocol.PortalTrainingMessage("通過挑戰，獲得 1,000,000 楓幣。"))
	}
	guard := func(c domain.CharacterSession) bool { return c.Character.MapID == from && c.Character.HP > 0 }
	return s.warpGameplayConditional(ctx, channel, live, sink, field.ID, entry, nil, guard, nil)
}

func (s *Server) joinObstacleEvent(ctx context.Context, channel int, live *liveCharacter, sink packetSink) error {
	space := s.quizzes
	if space == nil {
		return s.joinOXQuiz(ctx, channel, live, sink)
	}
	session := partySession(live)
	space.mu.Lock()
	run := space.obstacles[channel]
	if run == nil {
		space.mu.Unlock()
		return s.joinOXQuiz(ctx, channel, live, sink)
	}
	id := session.CharacterID
	_, joining := run.members[id]
	if joining || len(run.members) >= oxQuizCapacity || run.phase.Load() != obstacleRegistration || !time.Now().Before(run.deadline) ||
		session.Character.HP <= 0 || s.eventOf(id) != nil || s.tradeActive(live) || slices.Contains(run.event.maps, session.Character.MapID) {
		space.mu.Unlock()
		return sink.WritePacket(protocol.PortalTrainingMessage("本頻道目前無法進入活動。"))
	}
	field := run.fields[0]
	entry, _ := field.Portal("join00")
	run.members[id] = 0
	space.mu.Unlock()
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	guard := func(c domain.CharacterSession) bool {
		return run.phase.Load() == obstacleRegistration && c.Character.MapID == session.Character.MapID && c.Character.HP > 0
	}
	err := s.warpGameplayConditional(ctx, channel, live, sink, field.ID, entry, nil, guard, nil)
	space.mu.Lock()
	if partySession(live).Character.MapID == field.ID && run.phase.Load() != obstacleClosed {
		run.members[id] = field.ID
	} else {
		delete(run.members, id)
	}
	space.mu.Unlock()
	return err
}

func (s *Server) tickObstacles(now time.Time) {
	if s.quizzes == nil {
		return
	}
	s.quizzes.mu.Lock()
	defer s.quizzes.mu.Unlock()
	for channel, run := range s.quizzes.obstacles {
		if !now.Before(run.deadline) && run.phase.Swap(obstacleClosed) != obstacleClosed {
			go s.endObstacle(channel, run)
		}
	}
}

func (s *Server) endObstacle(channel int, run *obstacleRun) {
	for _, o := range s.eventOccupants(channel, run.event.maps) {
		s.exitObstacle(channel, o)
	}
	s.quizzes.mu.Lock()
	if s.quizzes.obstacles[channel] == run {
		delete(s.quizzes.obstacles, channel)
	}
	s.quizzes.mu.Unlock()
}

func (s *Server) exitObstacle(channel int, o eventOccupant) {
	to, err := s.gameplay.catalog.Map(oxQuizExit)
	if err != nil {
		return
	}
	if entry, ok := to.Spawn(); ok {
		ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		guard := func(c domain.CharacterSession) bool { return obstacleField(c.Character.MapID) }
		_ = s.warpGameplayConditional(ctx, channel, o.live, o.sink, to.ID, entry, nil, guard, nil)
	}
}
