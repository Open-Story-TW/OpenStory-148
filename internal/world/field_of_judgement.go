package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

const (
	judgementFare       = 1_000_000
	judgementInterval   = 4 * time.Hour
	judgementOpen       = 5 * time.Minute
	judgementClose      = 10 * time.Minute
	judgementStart      = 11 * time.Minute
	judgementDuration   = 20 * time.Minute
	judgementRewardTime = 2 * time.Minute
)

// The gameplay mutex protects admission and scores. runGameplay is the only
// writer of the scheduler notices and selected prizes.
type judgementSchedule struct {
	anchor           time.Time
	lastAnnouncement time.Time
	prizes           [2][7][]domain.ScriptDrop
}

type judgementState struct {
	setup       time.Time
	pending     int
	entrants    []int64
	scores      map[int64]int
	lotteryDone bool
	lotteryID   int64
	jackpot     int64
	jackpotPaid bool
	started     bool
	winner      int64
	winnerExit  time.Time
	prize       domain.ScriptDrop
	prizePlaced bool
}

func configuredJudgement(c *content.Catalog) (*judgementSchedule, error) {
	if os.Getenv("MAPLEGO_LOLCASTLE") != "1" {
		return nil, nil
	}
	now := time.Now()
	anchor := time.Date(now.Year(), now.Month(), now.Day(), 3, 50, 0, 0, now.Location())
	if value := os.Getenv("MAPLEGO_LOLCASTLE_ANCHOR"); value != "" {
		var err error
		anchor, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return nil, fmt.Errorf("MAPLEGO_LOLCASTLE_ANCHOR: %w", err)
		}
	}
	state := &judgementSchedule{anchor: anchor}
	if out, err := judgementPrizePools(c); err == nil {
		state.prizes = out
	} else {
		return nil, err
	}
	if _, err := c.Map(content.JudgementArena); err != nil {
		return nil, err
	}
	for room := content.JudgementRoomFirst; room < content.JudgementRoomFirst+content.JudgementRoomCount; room++ {
		for _, wave := range content.JudgementWaves(room) {
			if _, err := c.Mob(wave.TemplateID); err != nil {
				return nil, err
			}
		}
	}
	return state, nil
}

func (p *judgementSchedule) setupAt(now time.Time) time.Time {
	elapsed := now.Sub(p.anchor)
	remainder := elapsed % judgementInterval
	if remainder < 0 {
		remainder += judgementInterval
	}
	return now.Add(-remainder)
}

func (a *worldScript) judgementEnter(ctx context.Context, snapshot domain.CharacterSession, command scripting.Command) (scripting.CommandResult, error) {
	reply := func(result string) scripting.CommandResult { return scripting.CommandResult{Value: result} }
	if a.kind != "npc" || a.npcID != content.JudgementNPC || len(command.Ints) != 0 || command.Text != "" {
		return reply("closed"), domain.ErrForbidden
	}
	g := a.server.gameplay
	if g == nil || g.judgement == nil || snapshot.Character.MapID != content.JudgementLobby {
		return reply("closed"), nil
	}
	mapID := content.JudgementRoomForLevel(snapshot.Character.Level)
	if mapID == 0 {
		return reply("level"), nil
	}
	now := time.Now()
	setup := g.judgement.setupAt(now)
	if now.Before(setup.Add(judgementOpen)) || !now.Before(setup.Add(judgementClose)) {
		return reply("closed"), nil
	}
	key := mapKey{channel: snapshot.ChannelID, mapID: mapID}
	g.mu.Lock()
	room, err := g.room(key)
	if err != nil {
		g.mu.Unlock()
		return reply("closed"), err
	}
	if room.judgement == nil || !room.judgement.setup.Equal(setup) {
		if len(room.members) != 0 || (room.judgement != nil && room.judgement.pending != 0) {
			g.mu.Unlock()
			return reply("closed"), nil
		}
		room.mobs = make(map[int32]*gameplayMob)
		room.judgement = &judgementState{setup: setup, scores: make(map[int64]int)}
	}
	state := room.judgement
	state.pending++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		state.pending--
		g.mu.Unlock()
	}()

	result, err := a.takeTransitFare(ctx, command.ID, mapID, 0, judgementFare, func(current domain.CharacterSession) bool {
		return current.ChannelID == key.channel && current.Character.HP > 0 && current.Character.MapID == content.JudgementLobby &&
			content.JudgementRoomForLevel(current.Character.Level) == key.mapID && time.Now().Before(setup.Add(judgementClose))
	})
	if err != nil {
		return reply("closed"), err
	}
	if result.Value == true {
		return reply("entered"), nil
	}
	return reply("fare"), nil
}

func (s *Server) restoreJudgementLobby(ctx context.Context, snapshot *domain.CharacterSession) error {
	if !content.JudgementRoom(snapshot.Character.MapID) {
		return nil
	}
	lobby, err := s.gameplay.catalog.Map(content.JudgementLobby)
	if err != nil {
		return err
	}
	spawn, ok := lobby.Spawn()
	if !ok {
		return domain.ErrNotFound
	}
	value := snapshot.Character
	value.MapID, value.SpawnPoint, value.X, value.Y = lobby.ID, spawn.ID, spawn.X, spawn.Y
	value.HP = max(1, value.HP)
	if err := s.repo.SaveCharacter(ctx, *snapshot, value); err != nil {
		return err
	}
	value.Revision++
	snapshot.Character = value
	return nil
}

type judgementAction struct {
	key    mapKey
	state  *judgementState
	member *gameplayMember
	id     int64
	award  string
	exit   bool
	death  bool
	end    time.Time
	score  int
}

type judgementPacket struct {
	visit  *sceneVisit
	packet []byte
}

func (s *Server) tickJudgement(ctx context.Context, now time.Time) {
	g := s.gameplay
	if g.judgement == nil {
		return
	}
	p := g.judgement
	setup := p.setupAt(now)
	elapsed := now.Sub(setup)
	if elapsed >= judgementOpen && elapsed < judgementStart+time.Minute {
		minute := setup.Add(elapsed.Truncate(time.Minute))
		if !minute.Equal(p.lastAnnouncement) {
			p.lastAnnouncement = minute
			if elapsed < judgementClose {
				remaining := int((judgementClose - elapsed + time.Minute - 1) / time.Minute)
				s.broadcastNotice(0, 0, fmt.Sprintf("審判之地還有 %d 分鐘截止報名。", remaining))
			} else if elapsed >= judgementStart {
				s.broadcastNotice(0, 0, "審判之地開始。")
			}
		}
	}
	g.mu.Lock()
	var packets []judgementPacket
	var actions []judgementAction
	for key, room := range g.rooms {
		state := room.judgement
		if state == nil || state.pending > 0 {
			continue
		}
		age := now.Sub(state.setup)
		if !state.lotteryDone && age >= judgementClose {
			state.chooseJackpot(room)
		}
		if member := room.members[state.lotteryID]; member != nil && !state.jackpotPaid {
			actions = append(actions, judgementAction{key: key, state: state, id: state.lotteryID, member: member, award: "jackpot"})
		}
		if len(room.members) == 0 {
			if age >= judgementClose {
				delete(g.rooms, key)
			}
			continue
		}
		if !state.started && age >= judgementStart && age < judgementStart+judgementDuration {
			out, err := g.startJudgementLocked(key, room, now)
			if err != nil {
				slog.Warn("judgement_spawn_failed", "room", key.mapID, "error", err)
			} else {
				state.started = true
				packets = append(packets, out...)
			}
		}
		actions = append(actions, state.settlement(key, room, now)...)
		for id, member := range room.members {
			actions = append(actions, judgementAction{key: key, state: state, id: id, member: member, score: state.scores[id], death: true})
		}
	}
	g.mu.Unlock()
	for _, packet := range packets {
		_ = s.writeScenePackets(packet.visit, packet.packet)
	}
	for _, action := range actions {
		s.applyJudgementAction(ctx, now, action)
	}
}

func (state *judgementState) chooseJackpot(room *gameplayRoom) {
	var players []int64
	for _, id := range state.entrants {
		if room.members[id] != nil {
			players = append(players, id)
		}
	}
	if len(players) > 0 {
		state.lotteryID = players[rand.IntN(len(players))]
		state.jackpot = int64(500_000 * len(players))
	}
	state.lotteryDone = true
}

func (state *judgementState) settlement(key mapKey, room *gameplayRoom, now time.Time) []judgementAction {
	end := state.setup.Add(judgementStart + judgementDuration)
	if state.started && state.winnerExit.IsZero() && now.Before(end) {
		clear := true
		for _, mob := range room.mobs {
			clear = clear && mob.hp == 0
		}
		if clear {
			for _, id := range state.entrants {
				if room.members[id] != nil && (state.winner == 0 || state.scores[id] > state.scores[state.winner]) {
					state.winner = id
				}
			}
			state.winnerExit = now.Add(judgementRewardTime)
		}
	}
	var out []judgementAction
	for id, member := range room.members {
		action := judgementAction{key: key, state: state, member: member, id: id, score: state.scores[id]}
		closing := !state.winnerExit.IsZero() && id != state.winner
		if !state.winnerExit.IsZero() && id == state.winner {
			action.end = state.winnerExit
			action.award = "prize"
			action.exit = !now.Before(state.winnerExit)
			if (!state.prizePlaced && len(room.members) == 1) || action.exit {
				out = append(out, action)
			}
		} else if closing || !now.Before(end) {
			action.exit = true
			out = append(out, action)
		}
	}
	return out
}

func (s *Server) applyJudgementAction(ctx context.Context, now time.Time, a judgementAction) {
	if a.award != "" && !a.exit {
		s.awardJudgement(ctx, now, a)
		return
	}
	session := partySession(a.member.live)
	if session.CharacterID != a.id || session.ChannelID != a.key.channel || session.Character.MapID != a.key.mapID {
		return
	}
	if a.death && session.Character.HP > 0 {
		return
	}
	if a.award == "" {
		_ = s.writeScenePackets(a.member.visit, protocol.Notice(fmt.Sprintf("審判之地結束。你的分數為 %d。", a.score)))
	}
	lobby, err := s.gameplay.catalog.Map(content.JudgementLobby)
	if err != nil {
		return
	}
	entry, ok := lobby.Spawn()
	if !ok {
		return
	}
	var revive *revivalTransition
	if session.Character.HP <= 0 {
		revive = &revivalTransition{fromMap: a.key.mapID, hp: 1}
	}
	guard := func(c domain.CharacterSession) bool { return c.Character.MapID == a.key.mapID }
	_ = s.warpGameplayConditional(ctx, a.key.channel, a.member.live, a.member.sink, lobby.ID, entry, revive, guard, nil)
}

func (s *Server) awardJudgement(ctx context.Context, now time.Time, a judgementAction) {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return
	}
	g := s.gameplay
	state := a.state
	live := a.member.live
	live.persistMu.Lock()
	session := s.skillSession(live)
	visit := s.sceneFor(live, a.member.sink)
	if visit == nil || visit != a.member.visit || session.Character.MapID != a.key.mapID || session.Character.HP <= 0 {
		live.persistMu.Unlock()
		return
	}
	op := fmt.Sprintf("judgement-%d-%d-%d-%s", state.setup.Unix(), a.key.channel, a.key.mapID, a.award)
	request := domain.ScriptExchange{Session: session, OperationID: op, MesosDelta: state.jackpot}
	if a.award == "prize" {
		if state.prize.TemplateID == 0 {
			state.prize = g.judgement.rollPrize(a.key.mapID, 1+rand.IntN(100))
			state.prize.X, state.prize.Y = session.Character.X, session.Character.Y
			if field, err := g.catalog.Map(a.key.mapID); err == nil {
				if y, ok := groundBelow(field, state.prize.X, state.prize.Y); ok {
					state.prize.Y = y
				}
			}
		}
		if state.prize.TemplateID == 0 {
			live.persistMu.Unlock()
			return
		}
		request.MesosDelta = 0
		request.Drops = []domain.ScriptDrop{state.prize}
	}
	step, cancel := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.ExecuteScriptExchange(step, request)
	cancel()
	if err == nil {
		s.applyProgressResult(live, result.Character)
	}
	live.persistMu.Unlock()
	if err != nil {
		if !errors.Is(err, domain.ErrStaleOwner) {
			slog.Warn("judgement_reward_pending", "room", a.key.mapID, "reward", a.award, "error", err)
		}
		return
	}
	if a.award == "jackpot" {
		state.jackpotPaid = true
		s.broadcastNotice(a.key.channel, a.key.mapID, fmt.Sprintf("%s 抽中 %d 楓幣。", session.Character.Name, state.jackpot))
		_ = s.writeScenePackets(visit, protocol.UpdateMesos(result.Character.Mesos, true))
		return
	}
	state.prizePlaced = true
	seconds := int((a.end.Sub(now) + time.Second - 1) / time.Second)
	_ = s.writeScenePackets(visit, protocol.Clock(seconds), protocol.Notice(fmt.Sprintf("你以 %d 分獲勝，請在時間內拾取獎品。", a.score)))
	for _, drop := range result.Drops {
		s.broadcastCombatDrop(drop)
	}
}
