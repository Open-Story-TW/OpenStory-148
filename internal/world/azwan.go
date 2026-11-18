package world

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const azwanDailyLimit int64 = 10
const azwanFinishEXP int64 = 30000

var azwanDaily = domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogDaily, Name: "azwan_sweep_entries"}
var azwanHonor = domain.ScriptLogKey{Scope: domain.LogCharacter, Period: domain.LogPermanent, Name: "azwan_honor_experience"}

func (s *Server) startAzwanSweep(ctx context.Context, inst *eventInstance, rosterSize int) error {
	group := s.activeEventMembers(inst, content.AzwanSweepOne)
	if len(group) != rosterSize {
		return domain.ErrStaleOwner
	}
	maximum := azwanDailyLimit
	return s.exchangeEventRosterLog(ctx, inst, group, content.AzwanSweepOne, "azwan-entry", domain.ScriptLogChange{Key: azwanDaily, Delta: 1, Maximum: &maximum})
}

func (s *Server) azwanSweepPortal(ctx context.Context, actor domain.CharacterSession, live *liveCharacter, sink packetSink, portal content.Portal) (bool, error) {
	mapID := actor.Character.MapID
	if mapID != content.AzwanSweepOne && mapID != content.AzwanSweepTwo && mapID != content.AzwanSweepLast {
		return false, nil
	}
	final := mapID == content.AzwanSweepLast
	expected := "next_AswanStage"
	if final {
		expected = "Aswan_final"
	}
	if portal.Script != expected || portal.Name != "next00" || portal.Type != 7 {
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.definition.Name != content.AzwanSweepName || inst.channel != actor.ChannelID {
		return true, eventPortalNotice(sink, "請先從阿斯旺避難所報名。")
	}
	s.events.mu.Lock()
	active := !inst.starting && !inst.ended
	s.events.mu.Unlock()
	if !active || s.mapMonsterCount(inst.channel, mapID) > 0 {
		return true, eventPortalNotice(sink, "請先擊倒這一關的所有怪物。")
	}
	destination := content.AzwanObelisk
	if !final {
		destination = inst.definition.Maps[slices.Index(inst.definition.Maps, mapID)+1]
	}
	field, err := s.gameplay.catalog.Map(destination)
	if err != nil {
		return true, err
	}
	entry, ok := field.Spawn()
	if !ok {
		return true, domain.ErrNotFound
	}
	if !final {
		return true, s.warpEventMember(ctx, inst, mapID, eventOccupant{live, sink}, destination, entry)
	}
	reward, err := s.awardAzwanSweep(ctx, inst, live)
	if err != nil {
		return true, eventPortalNotice(sink, "暫時無法發放掃蕩獎勵，請再試一次。")
	}
	if err = s.warpEventMember(ctx, inst, mapID, eventOccupant{live, sink}, destination, entry); err != nil {
		return true, err
	}
	level, exp := azwanHonorLevel(reward.before + reward.amount)
	oldLevel, _ := azwanHonorLevel(reward.before)
	for _, packet := range [][]byte{
		protocol.UpdateCombatStats(partySession(live).Character), protocol.ShowExperienceGain(azwanFinishEXP, true),
		protocol.HonorProgress(level, exp, level > oldLevel),
		protocol.PortalTrainingMessage(fmt.Sprintf("完成掃蕩希拉殘黨，獲得 %d 名譽值。", reward.amount)),
	} {
		if err = sink.WritePacket(packet); err != nil {
			return true, err
		}
	}
	return true, sink.WritePacket(protocol.EnableActions())
}

type azwanSweepReward struct {
	before int64
	amount int64
}

func (s *Server) awardAzwanSweep(ctx context.Context, inst *eventInstance, live *liveCharacter) (azwanSweepReward, error) {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return azwanSweepReward{}, domain.ErrForbidden
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	actor := s.skillSession(live)
	if actor.ChannelID != inst.channel || actor.Character.MapID != content.AzwanSweepLast || actor.Character.HP <= 0 || s.eventOf(actor.CharacterID) != inst {
		return azwanSweepReward{}, domain.ErrStaleOwner
	}
	// Cache the terms so a retry after a failed warp uses the same durable receipt.
	s.events.mu.Lock()
	reward, reserved := inst.azwanSweepRewards[actor.CharacterID]
	s.events.mu.Unlock()
	if !reserved {
		logs, err := repo.ReadScriptLogs(ctx, actor, []domain.ScriptLogKey{azwanHonor})
		if err != nil {
			return reward, err
		}
		if len(logs) != 1 {
			return reward, domain.ErrNotFound
		}
		reward.before = logs[0].Value
		level, _ := azwanHonorLevel(reward.before)
		reward.amount = int64(level * 100)
		s.events.mu.Lock()
		inst.azwanSweepRewards[actor.CharacterID] = reward
		s.events.mu.Unlock()
	}
	result, err := repo.ExecuteScriptExchange(ctx, domain.ScriptExchange{
		Session: actor, OperationID: "event-" + inst.id + "-azwan-clear", EXP: azwanFinishEXP,
		Logs: []domain.ScriptLogChange{{Key: azwanHonor, Expected: &reward.before, Delta: reward.amount}},
	})
	if err != nil {
		return reward, err
	}
	s.applyProgressResult(live, result.Character)
	return reward, nil
}

// The v148 honor bar uses the points within the current level. Keep the
// cumulative value as one durable counter to make its award and EXP atomic.
func azwanHonorLevel(total int64) (level, experience int) {
	level = 1
	for required := int64(level+1) * 500; total >= required; required = int64(level+1) * 500 {
		total -= required
		level++
	}
	return level, int(total)
}

func (s *Server) sendAzwanHonor(ctx context.Context, actor domain.CharacterSession, sink packetSink) error {
	repo, ok := s.repo.(domain.ScriptRepository)
	if !ok {
		return nil
	}
	logs, err := repo.ReadScriptLogs(ctx, actor, []domain.ScriptLogKey{azwanHonor})
	if err != nil || len(logs) != 1 || logs[0].Value == 0 {
		return nil
	}
	level, exp := azwanHonorLevel(logs[0].Value)
	return sink.WritePacket(protocol.HonorProgress(level, exp, false))
}

func azwanStageEffect(sink packetSink, stages []int, mapID int) error {
	stage := slices.Index(stages, mapID) + 1
	if stage == len(stages) {
		return sink.WritePacket(protocol.FieldScreenEffect("aswan/stageEff/final"))
	}
	if err := sink.WritePacket(protocol.FieldScreenEffect("aswan/stageEff/stage")); err != nil {
		return err
	}
	return sink.WritePacket(protocol.FieldScreenEffect("aswan/stageEff/number/" + strconv.Itoa(stage)))
}
