package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type coconutRun struct {
	rules    content.CoconutRules
	entry    [2]content.Portal
	teams    map[int64]int
	lastHit  map[int64]time.Time
	targets  []coconutTarget
	score    [2]int
	deadline time.Time
	phase    atomic.Int32
	overtime bool
	winner   int
}

type coconutTarget struct {
	action int
	hits   int
}

const (
	coconutRegistration = iota
	coconutRunning
	coconutResults
	coconutClosed
)

func (s *Server) manageCoconut(channel int, action int64, now time.Time) string {
	if s.quizzes == nil || s.gameplay == nil {
		return "打椰子目前無法使用。"
	}
	space := s.quizzes
	space.mu.Lock()
	defer space.mu.Unlock()
	run := space.coconuts[channel]
	switch action {
	case 1:
		if run != nil || space.channels[channel] != nil || space.obstacles[channel] != nil || space.hunts[channel] != nil || space.snowballs[channel] != nil {
			return "本頻道已有活動。"
		}
		field, err := s.gameplay.catalog.Map(content.CoconutField)
		if err != nil {
			return "打椰子地圖尚未準備完成。"
		}
		rules, err := content.LoadCoconutRules(wzRoot())
		if err != nil {
			return "打椰子地圖缺少原版規則。"
		}
		var starts []content.Portal
		for _, p := range field.Portals {
			if p.Type == 0 {
				starts = append(starts, p)
			}
		}
		if len(starts) < 4 {
			return "打椰子地圖缺少隊伍入口。"
		}
		run = &coconutRun{
			rules: rules, entry: [2]content.Portal{starts[0], starts[2]},
			teams: make(map[int64]int), lastHit: make(map[int64]time.Time), deadline: now.Add(5 * time.Minute), winner: -1,
		}
		for i := range len(rules.Targets) {
			target := coconutTarget{action: 1}
			if i < rules.Bomb {
				target.action = 2
			} else if i < rules.Bomb+rules.Stop {
				target.action = 3
			}
			run.targets = append(run.targets, target)
		}
		rand.Shuffle(len(run.targets), func(i, j int) { run.targets[i], run.targets[j] = run.targets[j], run.targets[i] })
		space.coconuts[channel] = run
		go s.broadcastNotice(0, 0, fmt.Sprintf("頻道 %d 的打椰子開始報名，換到該頻道並輸入 @event 參加。", channel))
		return "打椰子已開放報名。"
	case 2:
		if run == nil || run.phase.Load() != coconutRegistration || !now.Before(run.deadline) {
			return "目前沒有可開始的打椰子活動。"
		}
		if !s.contestTeamsReady(channel, content.CoconutField, run.teams) {
			return "兩隊各至少需要一位玩家入場。"
		}
		run.deadline = now.Add(run.rules.Duration)
		run.phase.Store(coconutRunning)
		go s.broadcastCoconutStart(channel, int(run.rules.Duration.Seconds()))
		return "打椰子已開始。"
	case 3:
		if run == nil || run.phase.Swap(coconutClosed) == coconutClosed {
			return "目前沒有可取消的打椰子活動。"
		}
		go s.finishCoconut(channel, run)
		return "打椰子已取消。"
	}
	return "無效的活動指令。"
}

func (s *Server) broadcastCoconutStart(channel, seconds int) {
	for _, packet := range [][]byte{protocol.CoconutAction(-32768, 0, 0), protocol.CoconutScore([2]int{}), protocol.Clock(seconds)} {
		s.broadcastInventoryPacket(channel, content.CoconutField, packet)
	}
}

func (s *Server) joinCoconut(ctx context.Context, channel int, live *liveCharacter, sink packetSink) (bool, error) {
	space := s.quizzes
	if space == nil {
		return false, nil
	}
	session := partySession(live)
	space.mu.Lock()
	run := space.coconuts[channel]
	if run == nil {
		space.mu.Unlock()
		return false, nil
	}
	id := session.CharacterID
	_, joined := run.teams[id]
	if joined || len(run.teams) >= oxQuizCapacity || run.phase.Load() != coconutRegistration || !time.Now().Before(run.deadline) ||
		session.Character.HP <= 0 || session.Character.GM != 0 || s.eventOf(id) != nil || s.tradeActive(live) {
		space.mu.Unlock()
		return true, sink.WritePacket(protocol.PortalTrainingMessage("目前無法參加打椰子。"))
	}
	team := contestTeam(run.teams)
	run.teams[id] = team
	space.mu.Unlock()
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	guard := func(c domain.CharacterSession) bool {
		return run.phase.Load() == coconutRegistration && c.Character.MapID == session.Character.MapID && c.Character.HP > 0
	}
	err := s.warpGameplayConditional(ctx, channel, live, sink, content.CoconutField, run.entry[team], nil, guard, nil)
	if partySession(live).Character.MapID != content.CoconutField {
		space.mu.Lock()
		delete(run.teams, id)
		space.mu.Unlock()
	}
	return true, err
}

func (s *Server) coconutFieldInfo(session domain.CharacterSession) (*byte, int) {
	if session.Character.MapID != content.CoconutField || s.quizzes == nil {
		return nil, 0
	}
	space := s.quizzes
	space.mu.Lock()
	defer space.mu.Unlock()
	run := space.coconuts[session.ChannelID]
	if run == nil {
		return nil, 0
	}
	team, member := run.teams[session.CharacterID]
	if !member || run.phase.Load() == coconutClosed {
		return nil, 0
	}
	seconds := 0
	if run.phase.Load() == coconutRunning {
		seconds = max(0, int(time.Until(run.deadline).Seconds()))
	}
	value := byte(team)
	return &value, seconds
}

func (s *Server) coconutSetField(packet []byte, session domain.CharacterSession) ([]byte, *byte) {
	if session.Character.MapID != content.CoconutField {
		return packet, nil
	}
	team, seconds := s.coconutFieldInfo(session)
	if team == nil {
		unassigned := byte(255)
		team = &unassigned
	}
	return protocol.CoconutFieldInfo(packet, *team, seconds), team
}

func (s *Server) coconutEntered(channel int, live *liveCharacter, sink packetSink) {
	session := partySession(live)
	if session.Character.MapID != content.CoconutField {
		return
	}
	team, seconds := s.coconutFieldInfo(session)
	if team == nil {
		go s.exitContest(channel, content.CoconutField, eventOccupant{live, sink})
		return
	}
	s.quizzes.mu.Lock()
	run := s.quizzes.coconuts[channel]
	var score [2]int
	if run != nil {
		score = run.score
	}
	s.quizzes.mu.Unlock()
	_ = sink.WritePacket(protocol.CoconutScore(score))
	if seconds > 0 {
		_ = sink.WritePacket(protocol.Clock(seconds))
	}
}

func (s *Server) attackCoconut(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	target, delay, err := protocol.ReadCoconutHit(r)
	session := partySession(live)
	c := session.Character
	if err != nil || c.MapID != content.CoconutField || c.HP <= 0 || s.sceneFor(live, sink) == nil || s.quizzes == nil {
		return nil
	}
	space := s.quizzes
	space.mu.Lock()
	run := space.coconuts[session.ChannelID]
	now := time.Now()
	if run == nil || run.phase.Load() != coconutRunning || !now.Before(run.deadline) || target >= len(run.targets) {
		space.mu.Unlock()
		return nil
	}
	team, member := run.teams[c.ID]
	point := run.rules.Targets[target]
	nut := &run.targets[target]
	if !member || nut.hits >= run.rules.Hits || abs(c.X-point.X) > 180 || abs(c.Y-point.Y) > 180 ||
		now.Sub(run.lastHit[c.ID]) < 200*time.Millisecond || delay > 2000 {
		space.mu.Unlock()
		return nil
	}
	run.lastHit[c.ID] = now
	nut.hits++
	var action, score []byte
	if nut.hits == run.rules.Hits {
		action = protocol.CoconutAction(target, delay, nut.action)
		if nut.action == 1 {
			run.score[team]++
			score = protocol.CoconutScore(run.score)
		}
	}
	space.mu.Unlock()
	s.broadcastInventoryPacket(session.ChannelID, content.CoconutField, action)
	s.broadcastInventoryPacket(session.ChannelID, content.CoconutField, score)
	return nil
}

func (s *Server) tickCoconut(now time.Time) {
	if s.quizzes == nil {
		return
	}
	space := s.quizzes
	space.mu.Lock()
	defer space.mu.Unlock()
	for channel, run := range space.coconuts {
		if now.Before(run.deadline) {
			continue
		}
		switch run.phase.Load() {
		case coconutRunning:
			score := run.score
			if score[0] == score[1] && !run.overtime {
				run.overtime = true
				run.deadline = now.Add(run.rules.Overtime)
				go s.broadcastInventoryPacket(channel, content.CoconutField, protocol.Clock(int(run.rules.Overtime.Seconds())))
				continue
			}
			if score[0] > score[1] {
				run.winner = 0
			} else if score[1] > score[0] {
				run.winner = 1
			}
			run.deadline = now.Add(run.rules.Finish)
			run.phase.Store(coconutResults)
			go s.coconutResults(channel, run, run.winner)
		case coconutRegistration, coconutResults:
			run.phase.Store(coconutClosed)
			go s.finishCoconut(channel, run)
		}
	}
}

func (s *Server) coconutResults(channel int, run *coconutRun, winner int) {
	for _, o := range s.eventOccupants(channel, []int{content.CoconutField}) {
		team, _ := s.coconutFieldInfo(partySession(o.live))
		effect, sound := run.rules.LoseEffect, run.rules.LoseSound
		if team != nil && int(*team) == winner {
			effect, sound = run.rules.WinEffect, run.rules.WinSound
		}
		_ = s.writeScenePackets(s.sceneFor(o.live, o.sink), protocol.FieldScreenEffect(effect), protocol.FieldSoundEffect(sound))
	}
}

func (s *Server) finishCoconut(channel int, run *coconutRun) {
	for _, o := range s.eventOccupants(channel, []int{content.CoconutField}) {
		s.exitContest(channel, content.CoconutField, o)
	}
	s.quizzes.mu.Lock()
	if s.quizzes.coconuts[channel] == run {
		delete(s.quizzes.coconuts, channel)
	}
	s.quizzes.mu.Unlock()
}
