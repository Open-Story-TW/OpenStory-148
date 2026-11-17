package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type ludibriumState struct {
	mu        sync.Mutex
	boxes     [10]byte
	quiz      ludibriumQuestion
	rewardEXP map[int64]int64
}

type ludibriumQuestion struct {
	text string
	mask int64
}

func newLudibriumState() *ludibriumState {
	questions := [...]ludibriumQuestion{
		{"9 * 9 + 100 - 143 = ?", 1<<2 | 1<<7}, // 38
		{"3 * 8 + 10 = ?", 1<<2 | 1<<3},        // 34
	}
	l := &ludibriumState{quiz: questions[rand.IntN(len(questions))], rewardEXP: make(map[int64]int64)}
	for i := range l.boxes {
		l.boxes[i] = byte(rand.IntN(3)) + '0'
	}
	return l
}

type ludibriumStage struct {
	npc   int
	clear string
	exp   int64
}

var ludibriumStages = map[int]ludibriumStage{
	content.LudiPQFirst:  {2040036, "ludi1", 2100},
	content.LudiPQDark:   {2040039, "ludi2", 3360},
	content.LudiPQGolems: {2040042, "ludi4", 4620},
	content.LudiPQQuiz:   {2040043, "ludi5", 5040},
	content.LudiPQBoss:   {2040044, "ludi6", 0},
}

func (s *Server) checkLudibriumStage(ctx context.Context, actor domain.CharacterSession, npc int) (map[string]any, error) {
	id, mapID := actor.CharacterID, actor.Character.MapID
	stage, stageOK := ludibriumStages[mapID]
	inst := s.eventOf(id)
	result := map[string]any{"state": "unavailable"}
	if !stageOK || npc != stage.npc || inst == nil || inst.ludi == nil {
		return result, nil
	}
	if mapID == content.LudiPQQuiz {
		result["question"] = inst.ludi.quiz.text
	}
	if s.partySnapshotOf(id).LeaderID != id {
		result["state"] = "not_leader"
		return result, nil
	}
	inst.ludi.mu.Lock()
	defer inst.ludi.mu.Unlock()
	if s.eventOf(id) != inst {
		return result, nil
	}
	if s.eventProperty(id, stage.clear) == "ok" {
		result["state"] = "cleared"
		return result, nil
	}
	var ready bool
	switch mapID {
	case content.LudiPQDark:
		ready = true
		for m := 922010401; m <= 922010405; m++ {
			if s.mapMonsterCount(inst.channel, m) != 0 {
				ready = false
			}
		}
	case content.LudiPQQuiz:
		mask, err := s.eventAreaMask(ctx, inst, actor)
		if err != nil {
			return result, err
		}
		ready = mask == inst.ludi.quiz.mask
	default:
		ready = s.mapMonsterCount(inst.channel, mapID) == 0
	}
	if !ready {
		result["state"] = "pending"
		return result, nil
	}
	if mapID == content.LudiPQBoss {
		cleared, err := s.finishLudibrium(ctx, inst, actor)
		if err != nil || !cleared {
			return result, err
		}
	} else if err := s.awardEventStageEXP(ctx, inst, mapID, stage.exp); err != nil {
		return result, err
	}
	if err := s.setEventProperty(id, stage.clear, "ok"); err != nil {
		return result, err
	}
	s.ludibriumClearEffect(inst.channel, mapID)
	if mapID == content.LudiPQBoss {
		s.eventEndAt(inst, content.LudiPQClear)
		result["state"] = "finished"
	} else {
		result["state"] = "cleared"
	}
	return result, nil
}

func (s *Server) ludibriumClearEffect(channel, mapID int) {
	s.broadcastInventoryPacket(channel, mapID, protocol.FieldScreenEffect(eventEffects["clear"][0]))
	s.broadcastInventoryPacket(channel, mapID, protocol.FieldSoundEffect(eventEffects["clear"][1]))
	s.broadcastInventoryPacket(channel, mapID, protocol.FieldObjectEffect("gate"))
}

func (s *Server) finishLudibrium(ctx context.Context, inst *eventInstance, actor domain.CharacterSession) (bool, error) {
	occupants := s.eventOccupants(inst.channel, []int{content.LudiPQBoss})
	group := make([]eventOccupant, 0, len(occupants))
	for _, member := range occupants {
		session := partySession(member.live)
		if session.Character.HP > 0 && s.eventOf(session.CharacterID) == inst {
			group = append(group, member)
		}
	}
	if len(group) < inst.definition.Keep || actor.Character.HP <= 0 {
		return false, nil
	}
	for _, member := range group {
		err := s.applyEventExchange(ctx, member, "ludibrium-clear-"+inst.id, func(session domain.CharacterSession) (domain.ScriptExchange, error) {
			if s.eventOf(session.CharacterID) != inst || session.Character.MapID != content.LudiPQBoss {
				return domain.ScriptExchange{}, domain.ErrStaleOwner
			}
			// Legacy's six-stage clear uses givePartyExp_PQ(70, 1.0, players).
			id, level := session.CharacterID, max(1, session.Character.Level)
			if exp := inst.ludi.rewardEXP[id]; exp == 0 {
				scaled := level
				if level > 70 {
					scaled = 70 + (70-level)/10
				}
				limit := int64(min(level, 70))
				inst.ludi.rewardEXP[id] = (content.ExperienceForLevel(scaled)*5 + limit) / (2 * limit)
			}
			return domain.ScriptExchange{EXP: inst.ludi.rewardEXP[id],
				Logs: []domain.ScriptLogChange{{Key: domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "ludibrium-clear"}, Delta: 1}}}, nil
		})
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Server) ludibriumPortal(ctx context.Context, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	actor := partySession(live)
	c, room := actor.Character, actor.Character.MapID
	stage, next := "", 0
	wholeRoom := false
	switch {
	case room == content.LudiPQFirst && p.Name == "next00":
		stage, next, wholeRoom = "ludi1", content.LudiPQDark, true
	case room == content.LudiPQDark && p.Name == "next00":
		stage, next, wholeRoom = "ludi2", content.LudiPQBoxes, true
	case room == content.LudiPQBoxes && p.Script == "party2_rp":
		return true, s.ludibriumBox(ctx, actor, live, sink, p)
	case room == content.LudiPQBoxes && p.Name == "next00":
		stage, next, wholeRoom = "ludi3", content.LudiPQGolems, true
	case room == content.LudiPQGolems && p.Name == "next00":
		stage, next = "ludi4", content.LudiPQQuiz
	case room == content.LudiPQQuiz && p.Name == "next00":
		stage, next, wholeRoom = "ludi5", content.LudiPQBoss, true
	default:
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ludi == nil || s.eventProperty(actor.CharacterID, stage) != "ok" {
		_ = sink.WritePacket(protocol.PortalTrainingMessage("得先完成這一關才能開啟傳送點。"))
		return true, sink.WritePacket(protocol.EnableActions())
	}
	destination, err := s.gameplay.catalog.Map(next)
	if err != nil {
		return true, err
	}
	entry, ok := destination.Spawn()
	if !ok {
		return true, domain.ErrInvalidInput
	}
	mover := eventOccupant{live, sink}
	if !wholeRoom {
		s.cancelNPCScriptState(live, false)
		s.closeEconomyDialog(live)
		return true, s.warpEventMember(ctx, inst, c.MapID, mover, next, entry)
	}
	err = s.moveEventRoom(ctx, inst, room, mover, next, entry)
	return true, err
}

func (s *Server) ludibriumBox(ctx context.Context, actor domain.CharacterSession, live *liveCharacter, sink packetSink, p content.Portal) error {
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.ludi == nil || len(p.Name) != 4 || p.Name[:2] != "pt" ||
		p.Name[2] < '0' || p.Name[2] > '9' || p.Name[3] < '0' || p.Name[3] > '2' ||
		abs(actor.Character.X-p.X) > 34 || abs(actor.Character.Y-p.Y) > 55 {
		return sink.WritePacket(protocol.EnableActions())
	}
	m, err := s.gameplay.catalog.Map(content.LudiPQBoxes)
	if err != nil {
		return err
	}
	row := int(p.Name[2] - '0')
	portalName := "st00"
	correct := inst.ludi.boxes[row] == p.Name[3]
	if correct && row < 9 {
		portalName = fmt.Sprintf("pt%d0", row+1)
	}
	if correct && row == 9 {
		portalName = "success"
	}
	entry, ok := m.Portal(portalName)
	if !ok {
		return domain.ErrInvalidInput
	}
	mover := eventOccupant{live, sink}
	if correct {
		s.broadcastInventoryPacket(inst.channel, m.ID, protocol.FieldObjectEffect("an"+p.Name[2:]))
	}
	if portalName == "success" {
		if err = s.setEventProperty(actor.CharacterID, "ludi3", "ok"); err != nil {
			return err
		}
		s.ludibriumClearEffect(inst.channel, m.ID)
		return s.moveEventRoom(ctx, inst, m.ID, mover, m.ID, entry)
	}
	s.cancelNPCScriptState(live, false)
	s.closeEconomyDialog(live)
	return s.warpEventMember(ctx, inst, m.ID, mover, m.ID, entry)
}
