package world

import (
	"context"
	"sort"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// handlePhantomSteal handles the three v148 Phantom Skill Steal opcodes:
//   - 0x109 RecvTargetSkillList -> 0x33 SendTargetSkillListResult
//   - 0x108 RecvStealSkill      -> 0x32 SendStealSkillResult (add / delete)
//   - 0x107 RecvChooseSkill     -> 0x9C SendChooseSkillResult (equip / unequip)
func (s *Server) handlePhantomSteal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, op uint16, body []byte) error {
	r := protocol.NewReader(body)
	switch op {
	case protocol.RecvTargetSkillList:
		targetID, err := protocol.ReadTargetSkillList(r)
		if err != nil {
			return sink.WritePacket(protocol.TargetSkillListResult(0, 0, 0, nil))
		}
		return s.handlePhantomInspectPeer(channel, live, sink, targetID)
	case protocol.RecvStealSkill:
		req, err := protocol.ReadStealSkill(r)
		if err != nil {
			return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
		}
		return s.handlePhantomStealOrDelete(ctx, channel, live, sink, req)
	case protocol.RecvChooseSkill:
		req, err := protocol.ReadChooseSkill(r)
		if err != nil {
			return sink.WritePacket(protocol.EnableActions())
		}
		return s.handlePhantomChoose(ctx, live, sink, req)
	}
	return nil
}

func (s *Server) roomCharacterSnapshot(channel, mapID int, targetID int64) (domain.Character, bool) {
	if s.gameplay == nil {
		return domain.Character{}, false
	}
	s.gameplay.mu.Lock()
	defer s.gameplay.mu.Unlock()
	room := s.gameplay.rooms[mapKey{channel: channel, mapID: mapID}]
	if room == nil {
		return domain.Character{}, false
	}
	member := room.members[targetID]
	if member == nil || member.live == nil {
		return domain.Character{}, false
	}
	member.live.mu.Lock()
	c := member.live.session.Character
	c.Skills = append([]domain.SkillState(nil), c.Skills...)
	member.live.mu.Unlock()
	return c, true
}

func (s *Server) handlePhantomInspectPeer(channel int, live *liveCharacter, sink packetSink, targetID int64) error {
	live.mu.Lock()
	actor := live.session.Character
	live.mu.Unlock()
	if !domain.IsPhantomJob(actor.Job) || targetID == actor.ID {
		return sink.WritePacket(protocol.TargetSkillListResult(targetID, 0, 0, nil))
	}
	target, ok := s.roomCharacterSnapshot(channel, actor.MapID, targetID)
	if !ok || domain.PhantomJobTier(target.Job) == 0 {
		return sink.WritePacket(protocol.TargetSkillListResult(targetID, 0, 0, nil))
	}
	var skillIDs []int32
	for _, sk := range target.Skills {
		if sk.Level > 0 && domain.IsStealableExplorerSkill(sk.ID) {
			skillIDs = append(skillIDs, int32(sk.ID))
		}
	}
	sort.Slice(skillIDs, func(i, j int) bool { return skillIDs[i] < skillIDs[j] })
	return sink.WritePacket(protocol.TargetSkillListResult(target.ID, protocol.TargetSkillListSuccess, int32(target.Job), skillIDs))
}

func (s *Server) persistPhantomSteal(ctx context.Context, live *liveCharacter, next domain.PhantomStealState) error {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	session := live.session
	session.Lease = s.currentLease()
	live.mu.Unlock()
	if repo, ok := s.repo.(domain.PhantomStealRepository); ok {
		saved, err := repo.SavePhantomStealState(ctx, session, next)
		if err != nil {
			return err
		}
		live.mu.Lock()
		if live.session.Character.Revision == saved.Revision {
			live.session.Character.PhantomSteal = saved.PhantomSteal
		}
		live.mu.Unlock()
		return nil
	}
	live.mu.Lock()
	live.session.Character.PhantomSteal = next
	live.mu.Unlock()
	return nil
}

func (s *Server) handlePhantomStealOrDelete(ctx context.Context, channel int, live *liveCharacter, sink packetSink, req protocol.StealSkillRequest) error {
	live.mu.Lock()
	actor := live.session.Character
	state := actor.PhantomSteal
	live.mu.Unlock()
	if !domain.IsPhantomJob(actor.Job) {
		return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
	}

	if req.Action == 1 {
		idx, entry, ok := state.FindStolen(req.SkillID)
		if !ok {
			return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
		}
		state.Stolen[idx] = domain.PhantomStolenSkill{}
		for i, chosen := range state.Chosen {
			if chosen == req.SkillID {
				state.Chosen[i] = 0
			}
		}
		if err := s.persistPhantomSteal(ctx, live, state); err != nil {
			return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
		}
		return sink.WritePacket(protocol.StealSkillDeleted(int32(entry.Tier), int32(entry.Slot)))
	}

	// Action 0: steal skill from an Explorer peer in the same room.
	if req.TargetCharID <= 0 || req.TargetCharID == actor.ID || !domain.IsStealableExplorerSkill(req.SkillID) {
		return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
	}
	target, ok := s.roomCharacterSnapshot(channel, actor.MapID, req.TargetCharID)
	if !ok || domain.PhantomJobTier(target.Job) == 0 {
		return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
	}
	var targetSkill domain.SkillState
	found := false
	for _, sk := range target.Skills {
		if sk.ID == req.SkillID && sk.Level > 0 {
			targetSkill = sk
			found = true
			break
		}
	}
	if !found {
		return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
	}
	tier := domain.PhantomSkillTier(req.SkillID)
	cap := domain.PhantomSlotCapacity(tier)
	if cap == 0 {
		return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
	}
	slot := -1
	if _, existing, has := state.FindStolen(req.SkillID); has && existing.Tier == tier {
		slot = existing.Slot
	} else {
		for sIdx := 0; sIdx < cap; sIdx++ {
			flat, ok := domain.PhantomSlotOffset(tier, sIdx)
			if ok && state.Stolen[flat].SkillID == 0 {
				slot = sIdx
				break
			}
		}
	}
	if slot < 0 {
		return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailFull))
	}
	flat, _ := domain.PhantomSlotOffset(tier, slot)
	entry := domain.PhantomStolenSkill{
		Tier:        tier,
		Slot:        slot,
		SkillID:     req.SkillID,
		Level:       targetSkill.Level,
		MasterLevel: targetSkill.MasterLevel,
	}
	state.Stolen[flat] = entry
	if err := s.persistPhantomSteal(ctx, live, state); err != nil {
		return sink.WritePacket(protocol.StealSkillFailure(protocol.StealModeFailBad))
	}
	return sink.WritePacket(protocol.StealSkillSuccess(int32(tier), int32(slot), int32(entry.SkillID), int32(entry.Level), int32(entry.MasterLevel)))
}

func (s *Server) handlePhantomChoose(ctx context.Context, live *liveCharacter, sink packetSink, req protocol.ChooseSkillRequest) error {
	live.mu.Lock()
	actor := live.session.Character
	state := actor.PhantomSteal
	skillsCopy := append([]domain.SkillState(nil), actor.Skills...)
	live.mu.Unlock()
	if !domain.IsPhantomJob(actor.Job) {
		return sink.WritePacket(protocol.EnableActions())
	}
	baseTier := domain.PhantomBaseTier(req.BaseSkillID)
	if baseTier == 0 {
		return sink.WritePacket(protocol.EnableActions())
	}
	baseLearned := false
	for _, sk := range skillsCopy {
		if sk.ID == req.BaseSkillID && sk.Level > 0 {
			baseLearned = true
			break
		}
	}
	if !baseLearned {
		return sink.WritePacket(protocol.EnableActions())
	}
	if req.ChosenSkillID == 0 {
		state.Chosen[baseTier-1] = 0
		if err := s.persistPhantomSteal(ctx, live, state); err != nil {
			return sink.WritePacket(protocol.EnableActions())
		}
		return sink.WritePacket(protocol.ChooseSkillResult(int32(req.BaseSkillID), 0))
	}
	_, entry, ok := state.FindStolen(req.ChosenSkillID)
	if !ok || entry.Tier < 1 || entry.Tier > baseTier {
		return sink.WritePacket(protocol.EnableActions())
	}
	// Each stolen skill can occupy at most one Impeccable Memory slot.
	for i, existing := range state.Chosen {
		if existing == req.ChosenSkillID {
			state.Chosen[i] = 0
		}
	}
	state.Chosen[baseTier-1] = req.ChosenSkillID
	if err := s.persistPhantomSteal(ctx, live, state); err != nil {
		return sink.WritePacket(protocol.EnableActions())
	}
	return sink.WritePacket(protocol.ChooseSkillResult(int32(req.BaseSkillID), int32(req.ChosenSkillID)))
}
