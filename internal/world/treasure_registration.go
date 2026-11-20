package world

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

const treasureDuration = 10 * time.Minute

var treasureMapIDs = treasureFields()

type treasureHuntRun struct {
	id       string
	fields   []content.Map
	members  map[int64]int // expected field; zero while a member is joining
	course   map[int64]bool
	deadline time.Time
	phase    atomic.Int32 // registration, hunt and exit
}

const (
	treasureHuntRegistration = iota
	treasureHuntRunning
	treasureHuntClosed
)

func (s *Server) manageTreasureHunt(channel int, action int64, now time.Time) string {
	space := s.quizzes
	if space == nil || s.gameplay == nil {
		return "活動目前無法使用。"
	}
	space.mu.Lock()
	defer space.mu.Unlock()
	run := space.hunts[channel]
	switch action {
	case 1:
		if run != nil || space.channels[channel] != nil || space.obstacles[channel] != nil || space.snowballs[channel] != nil || space.coconuts[channel] != nil {
			return "本頻道已有活動。"
		}
		var fields []content.Map
		for _, id := range treasureMapIDs {
			field, err := s.gameplay.catalog.Map(id)
			if err != nil {
				return "活動地圖尚未準備完成。"
			}
			fields = append(fields, field)
		}
		if _, ok := fields[0].Portal("join00"); !ok {
			return "找不到活動入口。"
		}
		if err := s.gameplay.resetEventRooms(channel, content.Event{Maps: treasureMapIDs}); err != nil {
			return "尋寶場地還在使用中。"
		}
		run = &treasureHuntRun{id: platform.NewID(), fields: fields, members: make(map[int64]int), course: make(map[int64]bool), deadline: now.Add(5 * time.Minute)}
		space.hunts[channel] = run
		go s.broadcastNotice(0, 0, fmt.Sprintf("頻道 %d 的黃金傳說開始報名，換到該頻道並輸入 @event 參加。", channel))
		return "黃金傳說已開放報名。"
	case 2:
		if run == nil || run.phase.Load() != treasureHuntRegistration || !now.Before(run.deadline) {
			return "目前沒有可開始的活動。"
		}
		count := 0
		for _, o := range s.eventOccupants(channel, treasureMapIDs[:1]) {
			c := partySession(o.live).Character
			if run.members[c.ID] == c.MapID && c.HP > 0 && c.GM == 0 {
				count++
			}
		}
		if count < 2 {
			return "至少需要兩位玩家入場。"
		}
		run.deadline = now.Add(treasureDuration)
		run.phase.Store(treasureHuntRunning)
		go s.broadcastInventoryPacket(channel, content.TreasureHuntLobby, protocol.Clock(int(treasureDuration.Seconds())))
		return "黃金傳說已開始。"
	case 3:
		if run == nil || run.phase.Swap(treasureHuntClosed) == treasureHuntClosed {
			return "目前沒有可取消的活動。"
		}
		go s.endTreasureHunt(channel, run)
		return "黃金傳說已取消。"
	}
	return "無效的活動指令。"
}

func (s *Server) treasureAllowsEntry(session domain.CharacterSession, mapID int) bool {
	if !content.TreasureHuntMap(mapID) {
		return true
	}
	if s.quizzes == nil {
		return false
	}
	s.quizzes.mu.Lock()
	defer s.quizzes.mu.Unlock()
	run := s.quizzes.hunts[session.ChannelID]
	if run == nil || run.phase.Load() == treasureHuntClosed {
		return false
	}
	field, ok := run.members[session.CharacterID]
	return ok && (field == mapID || field == 0 && mapID == content.TreasureHuntLobby)
}

func (s *Server) treasureEntered(channel int, live *liveCharacter, sink packetSink) {
	session := partySession(live)
	if !content.TreasureHuntMap(session.Character.MapID) || s.quizzes == nil {
		return
	}
	if !s.treasureAllowsEntry(session, session.Character.MapID) {
		go s.exitTreasureHunt(channel, eventOccupant{live, sink})
		return
	}
	s.quizzes.mu.Lock()
	run := s.quizzes.hunts[channel]
	left := 0
	if run != nil && run.phase.Load() == treasureHuntRunning {
		left = int(time.Until(run.deadline).Seconds())
	}
	s.quizzes.mu.Unlock()
	if left > 0 {
		// The map-entry caller still owns the publication gate, as for the
		// party-quest clock. Do not acquire that gate a second time.
		_ = sink.WritePacket(protocol.Clock(left))
	}
}

func (s *Server) treasurePortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	session := partySession(live)
	from := session.Character.MapID
	if !content.TreasureHuntMap(from) {
		return false, nil
	}
	deny := func() (bool, error) { return true, sink.WritePacket(protocol.EnableActions()) }
	if s.quizzes == nil || s.tradeActive(live) {
		return deny()
	}
	space := s.quizzes
	space.mu.Lock()
	run := space.hunts[channel]
	if run == nil || run.phase.Load() != treasureHuntRunning || run.members[session.CharacterID] != from || !time.Now().Before(run.deadline) {
		space.mu.Unlock()
		return deny()
	}
	mapID, portalName := 0, ""
	if from == content.TreasureHuntLobby && p.Name == "join00" {
		mapID, portalName = from, p.TargetName
	} else if run.course[session.CharacterID] && p.TargetName != "" && p.Type >= 1 && p.Type <= 3 {
		mapID, portalName = p.TargetMap, p.TargetName
	} else if p.TargetMap == from && p.Type == 1 && p.TargetName != "" {
		mapID, portalName = from, p.TargetName
	} else {
		space.mu.Unlock()
		return deny()
	}
	to := slices.Index(treasureMapIDs, mapID)
	if to < 0 {
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
		return c.Character.MapID == from && c.Character.HP > 0 && run.phase.Load() == treasureHuntRunning
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

func (s *Server) joinGMEvent(ctx context.Context, channel int, live *liveCharacter, sink packetSink) error {
	if handled, err := s.joinSnowball(ctx, channel, live, sink); handled {
		return err
	}
	if handled, err := s.joinCoconut(ctx, channel, live, sink); handled {
		return err
	}
	space := s.quizzes
	if space == nil {
		return s.joinOXQuiz(ctx, channel, live, sink)
	}
	session := partySession(live)
	space.mu.Lock()
	if space.obstacles[channel] != nil {
		space.mu.Unlock()
		return s.joinObstacleEvent(ctx, channel, live, sink)
	}
	run := space.hunts[channel]
	if run == nil {
		space.mu.Unlock()
		return s.joinOXQuiz(ctx, channel, live, sink)
	}
	id := session.CharacterID
	_, joining := run.members[id]
	if joining || len(run.members) >= oxQuizCapacity || run.phase.Load() != treasureHuntRegistration || !time.Now().Before(run.deadline) ||
		session.Character.HP <= 0 || s.eventOf(id) != nil || s.tradeActive(live) || content.TreasureHuntMap(session.Character.MapID) {
		space.mu.Unlock()
		return sink.WritePacket(protocol.PortalTrainingMessage("本頻道目前無法進入活動。"))
	}
	field := run.fields[0]
	for _, item := range session.Character.Items {
		if item.TemplateID == 4031019 {
			space.mu.Unlock()
			return sink.WritePacket(protocol.PortalTrainingMessage("已持有惡魔文件，無法再次報名。"))
		}
	}
	entry, _ := field.Portal("join00")
	run.members[id] = 0
	space.mu.Unlock()
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	guard := func(c domain.CharacterSession) bool {
		return run.phase.Load() == treasureHuntRegistration && c.Character.MapID == session.Character.MapID && c.Character.HP > 0
	}
	err := s.warpGameplayConditional(ctx, channel, live, sink, field.ID, entry, nil, guard, nil)
	space.mu.Lock()
	if partySession(live).Character.MapID == field.ID && run.phase.Load() != treasureHuntClosed {
		run.members[id] = field.ID
	} else {
		delete(run.members, id)
	}
	space.mu.Unlock()
	return err
}

func (s *Server) tickTreasureHunts(now time.Time) {
	if s.quizzes == nil {
		return
	}
	s.quizzes.mu.Lock()
	defer s.quizzes.mu.Unlock()
	for channel, run := range s.quizzes.hunts {
		if !now.Before(run.deadline) && run.phase.Swap(treasureHuntClosed) != treasureHuntClosed {
			go s.endTreasureHunt(channel, run)
		}
	}
}

func (s *Server) endTreasureHunt(channel int, run *treasureHuntRun) {
	for _, o := range s.eventOccupants(channel, treasureMapIDs) {
		s.exitTreasureHunt(channel, o)
	}
	s.quizzes.mu.Lock()
	if s.quizzes.hunts[channel] == run {
		delete(s.quizzes.hunts, channel)
	}
	s.quizzes.mu.Unlock()
}

func (s *Server) exitTreasureHunt(channel int, o eventOccupant) {
	to, err := s.gameplay.catalog.Map(oxQuizExit)
	if err != nil {
		return
	}
	if entry, ok := to.Spawn(); ok {
		ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		guard := func(c domain.CharacterSession) bool { return content.TreasureHuntMap(c.Character.MapID) }
		_ = s.warpGameplayConditional(ctx, channel, o.live, o.sink, to.ID, entry, nil, guard, nil)
	}
}
