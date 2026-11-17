package world

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

const (
	oxQuizMap           = 109020001
	oxQuizExit          = 109050001
	oxQuizMinimum       = 2
	oxQuizCapacity      = 250
	oxQuizRoundCount    = 10
	oxQuizAnswerSeconds = 10
)

type oxQuiz struct {
	id         string
	field      content.Map
	questions  []content.OXQuestion
	members    map[int64]bool // true after the participant loads the field
	round      int
	deadline   time.Time
	accepting  atomic.Bool
	started    bool
	revealed   bool
	processing bool
}

type oxQuizSpace struct {
	mu       sync.Mutex
	channels map[int]*oxQuiz
	hunts    map[int]*treasureHuntRun
}

func newOXQuizSpace() *oxQuizSpace {
	return &oxQuizSpace{channels: make(map[int]*oxQuiz), hunts: make(map[int]*treasureHuntRun)}
}

func (s *Server) oxQuizInProgress(channel int) bool {
	if s.quizzes == nil {
		return false
	}
	s.quizzes.mu.Lock()
	defer s.quizzes.mu.Unlock()
	q := s.quizzes.channels[channel]
	return q != nil && q.started
}

func (s *Server) manageOXQuiz(channel int, action int64) string {
	if s.quizzes == nil || s.gameplay == nil {
		return "OX 問答目前無法使用。"
	}
	space := s.quizzes
	space.mu.Lock()
	q := space.channels[channel]
	switch action {
	case 1:
		if q != nil || space.hunts[channel] != nil {
			space.mu.Unlock()
			return "本頻道已有 OX 問答。"
		}
		field, err := s.gameplay.catalog.Map(oxQuizMap)
		if err != nil || len(field.Areas) != 2 {
			space.mu.Unlock()
			return "找不到完整的 OX 問答地圖。"
		}
		questions, err := s.gameplay.catalog.OXQuestions()
		if err != nil {
			space.mu.Unlock()
			return "無法讀取 OX 問答題庫。"
		}
		rand.Shuffle(len(questions), func(i, j int) { questions[i], questions[j] = questions[j], questions[i] })
		q = &oxQuiz{
			id: platform.NewID(), field: field, questions: questions[:oxQuizRoundCount], members: make(map[int64]bool),
			deadline: time.Now().Add(5 * time.Minute),
		}
		q.accepting.Store(true)
		space.channels[channel] = q
		space.mu.Unlock()
		s.broadcastNotice(0, 0, fmt.Sprintf("頻道 %d 的 OX 問答開始報名，換到該頻道並輸入 @event 參加。", channel))
		return "OX 問答已開放報名。"
	case 2:
		if q == nil || q.started || q.processing {
			space.mu.Unlock()
			return "目前沒有可開始的 OX 問答。"
		}
		var count int
		for _, member := range s.eventOccupants(channel, []int{oxQuizMap}) {
			session := partySession(member.live)
			if q.members[session.CharacterID] && session.Character.HP > 0 && session.Character.GM == 0 {
				count++
			}
		}
		if count < oxQuizMinimum {
			space.mu.Unlock()
			return "至少需要兩位玩家入場。"
		}
		q.started = true
		q.accepting.Store(false)
		q.deadline = time.Now().Add(oxQuizAnswerSeconds * time.Second)
		question := q.questions[0]
		space.mu.Unlock()
		s.showOXQuestion(channel, question)
		return "OX 問答第一題已開始。"
	case 3:
		if q == nil || q.processing {
			space.mu.Unlock()
			return "目前沒有可取消的 OX 問答。"
		}
		q.processing = true
		q.accepting.Store(false)
		space.mu.Unlock()
		s.finishOXQuiz(channel, q, "OX 問答已取消。")
		return "OX 問答已取消。"
	default:
		space.mu.Unlock()
		return "無效的 OX 問答指令。"
	}
}

func (s *Server) joinOXQuiz(ctx context.Context, channel int, live *liveCharacter, sink packetSink) error {
	noEntry := func() error {
		return sink.WritePacket(protocol.PortalTrainingMessage("本頻道的 OX 問答目前無法入場。"))
	}
	if s.quizzes == nil || s.tradeActive(live) {
		return noEntry()
	}
	session := partySession(live)
	if session.Character.HP <= 0 || session.Character.MapID == oxQuizMap || s.eventOf(session.CharacterID) != nil {
		return noEntry()
	}
	space := s.quizzes
	space.mu.Lock()
	q := space.channels[channel]
	if q == nil || q.started || q.processing || !q.deadline.After(time.Now()) || len(q.members) >= oxQuizCapacity {
		space.mu.Unlock()
		return noEntry()
	}
	spawn, ok := q.field.Spawn()
	if !ok {
		space.mu.Unlock()
		return noEntry()
	}
	q.members[session.CharacterID] = false
	space.mu.Unlock()
	guard := func(current domain.CharacterSession) bool {
		return q.accepting.Load() && current.ChannelID == channel &&
			current.Character.MapID == session.Character.MapID && current.Character.HP > 0
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	err := s.warpGameplayConditional(ctx, channel, live, sink, oxQuizMap, spawn, nil, guard, nil)
	entered := partySession(live).Character.MapID == oxQuizMap
	space.mu.Lock()
	active := space.channels[channel] == q && !q.started && !q.processing
	if entered && active {
		q.members[session.CharacterID] = true
	} else {
		delete(q.members, session.CharacterID)
	}
	space.mu.Unlock()
	if entered && !active {
		go s.exitOXQuiz(channel, eventOccupant{live, sink})
	}
	return err
}

func (s *Server) showOXQuestion(channel int, q content.OXQuestion) {
	s.broadcastNotice(channel, oxQuizMap, "OX 問答　"+q.Text)
	s.broadcastInventoryPacket(channel, oxQuizMap, protocol.OXQuiz(true, q.Set, q.ID))
	s.broadcastInventoryPacket(channel, oxQuizMap, protocol.Clock(oxQuizAnswerSeconds))
}

func (s *Server) oxQuizEntered(channel int, live *liveCharacter, sink packetSink) {
	session := partySession(live)
	if s.quizzes == nil || session.Character.MapID != oxQuizMap || session.Character.GM > 0 {
		return
	}
	var admitted bool
	s.quizzes.mu.Lock()
	if q := s.quizzes.channels[channel]; q != nil {
		_, admitted = q.members[session.CharacterID]
	}
	s.quizzes.mu.Unlock()
	if !admitted {
		go s.exitOXQuiz(channel, eventOccupant{live, sink})
	}
}

func (s *Server) tickOXQuizzes(now time.Time) {
	if s.quizzes == nil {
		return
	}
	space := s.quizzes
	space.mu.Lock()
	defer space.mu.Unlock()
	for channel, quiz := range space.channels {
		if !quiz.deadline.After(now) && !quiz.processing {
			quiz.processing = true
			quiz.accepting.Store(false)
			go s.advanceOXQuiz(channel, quiz)
		}
	}
}

func (s *Server) advanceOXQuiz(channel int, quiz *oxQuiz) {
	if !quiz.started {
		s.finishOXQuiz(channel, quiz, "OX 問答報名已結束。")
		return
	}
	q := quiz.questions[quiz.round]
	space := s.quizzes
	if quiz.revealed {
		space.mu.Lock()
		quiz.round++
		next := quiz.questions[quiz.round]
		quiz.deadline = time.Now().Add(oxQuizAnswerSeconds * time.Second)
		quiz.revealed, quiz.processing = false, false
		space.mu.Unlock()
		s.showOXQuestion(channel, next)
		return
	}
	s.broadcastInventoryPacket(channel, oxQuizMap, protocol.OXQuiz(false, q.Set, q.ID))
	correctSide := 1
	answer := "X"
	if q.True {
		correctSide = 0
		answer = "O"
	}
	s.broadcastNotice(channel, oxQuizMap, "正確答案是 "+answer+"。")
	winners := make([]eventOccupant, 0)
	var eliminated []eventOccupant
	for _, member := range s.eventOccupants(channel, []int{oxQuizMap}) {
		session := partySession(member.live)
		space.mu.Lock()
		registered := quiz.members[session.CharacterID]
		space.mu.Unlock()
		if !registered || session.Character.GM > 0 || session.Character.HP <= 0 {
			continue
		}
		x, y := session.Character.X, session.Character.Y
		ground, onGround := groundBelow(quiz.field, x, y)
		if quiz.field.Areas[correctSide].Contains(x, y) && onGround && ground-y <= 4 {
			winners = append(winners, member)
			s.creditOXQuiz(quiz, member, fmt.Sprintf("round-%d", quiz.round), 3000, 0)
		} else {
			_ = member.sink.WritePacket(protocol.PortalTrainingMessage("答錯了，下次再挑戰吧。"))
			space.mu.Lock()
			delete(quiz.members, session.CharacterID)
			space.mu.Unlock()
			eliminated = append(eliminated, member)
		}
	}
	if len(winners) < oxQuizMinimum || quiz.round+1 == len(quiz.questions) {
		for _, winner := range winners {
			s.creditOXQuiz(quiz, winner, "winner", 0, 1000000)
		}
		s.finishOXQuiz(channel, quiz, "OX 問答已結束。")
		return
	}
	space.mu.Lock()
	quiz.deadline = time.Now().Add(5 * time.Second)
	quiz.revealed, quiz.processing = true, false
	space.mu.Unlock()
	for _, member := range eliminated {
		go s.exitOXQuiz(channel, member)
	}
}

func (s *Server) creditOXQuiz(quiz *oxQuiz, member eventOccupant, action string, exp, mesos int64) {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	live := member.live
	live.persistMu.Lock()
	session := s.skillSession(live)
	visit := s.sceneFor(live, member.sink)
	if visit == nil || session.Character.MapID != oxQuizMap {
		live.persistMu.Unlock()
		return
	}
	result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{
		Session: session, OperationID: "ox-quiz-" + quiz.id + "-" + action, EXP: exp, MesosDelta: mesos,
	})
	if err == nil {
		s.applyProgressResult(live, result.Character)
	}
	live.persistMu.Unlock()
	if err != nil {
		slog.Warn("ox_quiz_reward_failed", "character", session.CharacterID, "error", err)
		return
	}
	if result.Replayed {
		return
	}
	if exp > 0 {
		_ = s.writeScenePackets(visit, protocol.UpdateCombatStats(result.Character), protocol.ShowExperienceGain(exp, true))
	}
	if mesos > 0 {
		_ = s.writeScenePackets(visit, protocol.UpdateMesos(result.Character.Mesos, true), protocol.PortalTrainingMessage("恭喜贏得 OX 問答，獲得 1,000,000 楓幣。"))
	}
}

func (s *Server) finishOXQuiz(channel int, quiz *oxQuiz, message string) {
	s.broadcastNotice(channel, oxQuizMap, message)
	for _, member := range s.eventOccupants(channel, []int{oxQuizMap}) {
		s.exitOXQuiz(channel, member)
	}
	s.quizzes.mu.Lock()
	if s.quizzes.channels[channel] == quiz {
		delete(s.quizzes.channels, channel)
	}
	s.quizzes.mu.Unlock()
}

func (s *Server) exitOXQuiz(channel int, member eventOccupant) {
	exit, err := s.gameplay.catalog.Map(oxQuizExit)
	if err != nil {
		return
	}
	spawn, ok := exit.Spawn()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guard := func(session domain.CharacterSession) bool {
		return session.ChannelID == channel && session.Character.MapID == oxQuizMap
	}
	if err := s.warpGameplayConditional(ctx, channel, member.live, member.sink, oxQuizExit, spawn, nil, guard, nil); err != nil {
		slog.Warn("ox_quiz_exit_failed", "error", err)
	}
}
