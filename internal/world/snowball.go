package world

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type snowballRun struct {
	rules            content.SnowballRules
	entry            [2]content.Portal
	teams            map[int64]int
	lastHit          map[int64]time.Time
	progress         [2]int
	health           [2]int
	frozenUntil      [2]time.Time
	registrationEnds time.Time
	phase            atomic.Int32
}

const (
	snowballRegistration = iota
	snowballRunning
	snowballClosed
)

func (s *Server) manageSnowball(channel int, action int64, now time.Time) string {
	if s.quizzes == nil || s.gameplay == nil {
		return "滾雪球目前無法使用。"
	}
	space := s.quizzes
	space.mu.Lock()
	defer space.mu.Unlock()
	run := space.snowballs[channel]
	switch action {
	case 1:
		if run != nil || space.channels[channel] != nil || space.obstacles[channel] != nil || space.hunts[channel] != nil || space.coconuts[channel] != nil {
			return "本頻道已有活動。"
		}
		field, err := s.gameplay.catalog.Map(content.SnowballField)
		if err != nil {
			return "滾雪球地圖尚未準備完成。"
		}
		rules, err := content.LoadSnowballRules(wzRoot())
		if err != nil {
			return "滾雪球地圖缺少原版規則。"
		}
		run = &snowballRun{teams: make(map[int64]int), lastHit: make(map[int64]time.Time), rules: rules, registrationEnds: now.Add(5 * time.Minute)}
		for i, lane := range rules.Lanes {
			entry, ok := field.Portal(lane.Entry)
			if !ok {
				return "滾雪球地圖缺少隊伍入口。"
			}
			run.entry[i], run.health[i] = entry, rules.ManHP
		}
		space.snowballs[channel] = run
		go s.broadcastNotice(0, 0, fmt.Sprintf("頻道 %d 的滾雪球開始報名，換到該頻道並輸入 @event 參加。", channel))
		return "滾雪球已開放報名。"
	case 2:
		if run == nil || run.phase.Load() != snowballRegistration || !now.Before(run.registrationEnds) {
			return "目前沒有可開始的滾雪球。"
		}
		if !s.contestTeamsReady(channel, content.SnowballField, run.teams) {
			return "兩隊各至少需要一位玩家入場。"
		}
		// The client snaps positions only while waiting. Refresh the two
		// loaded sprites before switching them to animated movement.
		prepare := run.statePacket(false)
		run.phase.Store(snowballRunning)
		packet := run.statePacket(false)
		go func() {
			s.broadcastInventoryPacket(channel, content.SnowballField, prepare)
			s.broadcastInventoryPacket(channel, content.SnowballField, packet)
		}()
		return "滾雪球已開始。"
	case 3:
		if run == nil || run.phase.Swap(snowballClosed) == snowballClosed {
			return "目前沒有可取消的滾雪球。"
		}
		go s.finishSnowball(channel, run, -1)
		return "滾雪球已取消。"
	}
	return "無效的活動指令。"
}

func (run *snowballRun) statePacket(initial bool) []byte {
	var damage *[3]int
	if initial {
		damage = &run.rules.Damage
	}
	return protocol.SnowballState(byte(run.phase.Load()), run.healthPercent(), run.progress, damage)
}

func (run *snowballRun) healthPercent() [2]int {
	return [2]int{run.health[0] * 100 / run.rules.ManHP, run.health[1] * 100 / run.rules.ManHP}
}

func (s *Server) joinSnowball(ctx context.Context, channel int, live *liveCharacter, sink packetSink) (bool, error) {
	space := s.quizzes
	if space == nil {
		return false, nil
	}
	session := partySession(live)
	space.mu.Lock()
	run := space.snowballs[channel]
	if run == nil {
		space.mu.Unlock()
		return false, nil
	}
	id := session.CharacterID
	_, joined := run.teams[id]
	if joined || len(run.teams) >= oxQuizCapacity || run.phase.Load() != snowballRegistration || !time.Now().Before(run.registrationEnds) ||
		session.Character.HP <= 0 || session.Character.GM != 0 || s.eventOf(id) != nil || s.tradeActive(live) {
		space.mu.Unlock()
		return true, sink.WritePacket(protocol.PortalTrainingMessage("目前無法參加滾雪球。"))
	}
	team := contestTeam(run.teams)
	run.teams[id] = team
	space.mu.Unlock()
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	guard := func(c domain.CharacterSession) bool {
		return run.phase.Load() == snowballRegistration && c.Character.MapID == session.Character.MapID && c.Character.HP > 0
	}
	err := s.warpGameplayConditional(ctx, channel, live, sink, content.SnowballField, run.entry[team], nil, guard, nil)
	if partySession(live).Character.MapID != content.SnowballField {
		space.mu.Lock()
		delete(run.teams, id)
		space.mu.Unlock()
	}
	return true, err
}

func (s *Server) snowballAllowsEntry(session domain.CharacterSession) bool {
	if s.quizzes == nil {
		return false
	}
	s.quizzes.mu.Lock()
	defer s.quizzes.mu.Unlock()
	run := s.quizzes.snowballs[session.ChannelID]
	if run == nil || run.phase.Load() == snowballClosed {
		return false
	}
	_, ok := run.teams[session.CharacterID]
	return ok
}

func (s *Server) snowballEntered(channel int, live *liveCharacter, sink packetSink) {
	session := partySession(live)
	if session.Character.MapID != content.SnowballField {
		return
	}
	if !s.snowballAllowsEntry(session) {
		go s.exitContest(channel, content.SnowballField, eventOccupant{live, sink})
		return
	}
	s.quizzes.mu.Lock()
	run := s.quizzes.snowballs[channel]
	var packet []byte
	if run != nil {
		packet = run.statePacket(true)
	}
	s.quizzes.mu.Unlock()
	if packet != nil {
		// The map switch is already publishing this scene.
		_ = sink.WritePacket(packet)
	}
}

func (s *Server) attackSnowball(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	target, damage, delay, err := protocol.ReadSnowballHit(r)
	session := partySession(live)
	c := session.Character
	if err != nil || c.MapID != content.SnowballField || c.HP <= 0 || s.sceneFor(live, sink) == nil || s.quizzes == nil {
		return nil
	}
	space := s.quizzes
	space.mu.Lock()
	run := space.snowballs[session.ChannelID]
	if run == nil || run.phase.Load() != snowballRunning {
		space.mu.Unlock()
		return nil
	}
	now := time.Now()
	team, ok := run.teams[c.ID]
	if !ok || target%2 != team || abs(c.Y-run.rules.Lanes[team].Y) > 85 || now.Sub(run.lastHit[c.ID]) < 200*time.Millisecond || delay > 2000 {
		space.mu.Unlock()
		return nil
	}
	run.lastHit[c.ID] = now
	var packets [][]byte
	winner := -1
	if target < 2 {
		x := run.rules.BallX + run.rules.Step*run.progress[team]
		if damage == run.rules.Damage[0] && abs(c.X-x) <= 230 && !now.Before(run.frozenUntil[team]) {
			run.progress[team]++
			for section, marker := range run.rules.Sections {
				if run.progress[team] == marker {
					packets = append(packets, protocol.SnowballMessage(team, section+1))
				}
			}
			if run.progress[team] >= run.rules.Finish {
				winner = team
				run.phase.Store(snowballClosed)
			}
		} else {
			damage = 0
		}
	} else if abs(c.X-run.rules.ManX) <= 170 && (damage == run.rules.Damage[1] || damage == run.rules.Damage[2]) {
		run.health[team] -= damage
		if run.health[team] <= 0 {
			run.health[team] = run.rules.Recovery
			run.frozenUntil[1-team] = now.Add(run.rules.Freeze)
			packets = append(packets, protocol.SnowballMessage(1-team, 4))
		}
	} else {
		damage = 0
	}
	state := run.statePacket(false)
	if winner == 1 {
		state = protocol.SnowballState(3, run.healthPercent(), run.progress, nil)
	}
	packets = append(packets, protocol.SnowballHit(target, damage, delay), state)
	space.mu.Unlock()
	for _, packet := range packets {
		s.broadcastInventoryPacket(session.ChannelID, content.SnowballField, packet)
	}
	if winner >= 0 {
		go s.finishSnowball(session.ChannelID, run, winner)
	}
	return nil
}

func (s *Server) tickSnowball(now time.Time) {
	if s.quizzes == nil {
		return
	}
	space := s.quizzes
	space.mu.Lock()
	defer space.mu.Unlock()
	for channel, run := range space.snowballs {
		switch run.phase.Load() {
		case snowballRegistration:
			if !now.Before(run.registrationEnds) {
				run.phase.Store(snowballClosed)
				go s.finishSnowball(channel, run, -1)
			}
		case snowballRunning:
			if len(s.eventOccupants(channel, []int{content.SnowballField})) == 0 {
				run.phase.Store(snowballClosed)
				go s.finishSnowball(channel, run, -1)
				continue
			}
			for team, until := range run.frozenUntil {
				if !until.IsZero() && !now.Before(until) {
					run.frozenUntil[team] = time.Time{}
					go s.broadcastInventoryPacket(channel, content.SnowballField, protocol.SnowballMessage(team, 5))
				}
			}
		}
	}
}

func (s *Server) finishSnowball(channel int, run *snowballRun, winner int) {
	if winner >= 0 {
		name := [...]string{"Story", "Maple"}[winner]
		s.broadcastNotice(channel, content.SnowballField, fmt.Sprintf("%s 隊贏得滾雪球。", name))
	}
	for _, o := range s.eventOccupants(channel, []int{content.SnowballField}) {
		s.exitContest(channel, content.SnowballField, o)
	}
	s.quizzes.mu.Lock()
	if s.quizzes.snowballs[channel] == run {
		delete(s.quizzes.snowballs, channel)
	}
	s.quizzes.mu.Unlock()
}

func (s *Server) exitContest(channel, mapID int, o eventOccupant) {
	field, err := s.gameplay.catalog.Map(oxQuizExit)
	if err != nil {
		return
	}
	if entry, ok := field.Spawn(); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		guard := func(c domain.CharacterSession) bool {
			return c.ChannelID == channel && c.Character.MapID == mapID
		}
		_ = s.warpGameplayConditional(ctx, channel, o.live, o.sink, field.ID, entry, nil, guard, nil)
	}
}

func contestTeam(teams map[int64]int) int {
	var players [2]int
	for _, team := range teams {
		players[team]++
	}
	if players[1] < players[0] {
		return 1
	}
	return 0
}

func (s *Server) contestTeamsReady(channel, mapID int, teams map[int64]int) bool {
	var players [2]int
	for _, o := range s.eventOccupants(channel, []int{mapID}) {
		c := partySession(o.live).Character
		if team, ok := teams[c.ID]; ok && c.GM == 0 && c.HP > 0 {
			players[team]++
		}
	}
	return players[0] > 0 && players[1] > 0
}
