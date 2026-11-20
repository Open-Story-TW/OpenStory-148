package protocol

import "maple-go-server/internal/domain"

const (
	// Client -> Server Phantom Skill Steal opcodes (v148 IDA-verified).
	RecvChooseSkill     uint16 = 0x107 // CUserLocal::SendChooseSkillReq (0x9345AD)
	RecvStealSkill      uint16 = 0x108 // CUserLocal::SendStealSkillReq (0x92FA8C)
	RecvTargetSkillList uint16 = 0x109 // Inspect peer stealable skills

	// Server -> Client Phantom Skill Steal opcodes (v148 CWvsContext::OnPacket 0x9C3271).
	SendStealSkillResult      uint16 = 0x32 // sub_9D28F4
	SendTargetSkillListResult uint16 = 0x33 // sub_9D2EAD
	SendChooseSkillResult     uint16 = 0x9C // sub_9CC384
)

const (
	StealModeAdd        byte = 0
	StealModeFailFull   byte = 1
	StealModeFailBad    byte = 2
	StealModeDeleteSlot byte = 3
	StealModeClearTier  byte = 4
	StealModeClearAll   byte = 5

	TargetSkillListSuccess int32 = 4
)

type StealSkillRequest struct {
	SkillID      int
	TargetCharID int64
	Action       byte // 0 = steal, 1 = delete
}

type ChooseSkillRequest struct {
	BaseSkillID   int
	ChosenSkillID int // 0 = unequip
}

func ReadTargetSkillList(r *Reader) (int64, error) {
	targetID := r.I32()
	if r.Err() != nil || r.Remaining() != 0 || targetID <= 0 {
		return 0, ErrMalformed
	}
	return int64(targetID), nil
}

func ReadStealSkill(r *Reader) (StealSkillRequest, error) {
	skillID := r.I32()
	targetID := r.I32()
	action := r.U8()
	if r.Err() != nil || r.Remaining() != 0 || skillID <= 0 || action > 1 {
		return StealSkillRequest{}, ErrMalformed
	}
	return StealSkillRequest{
		SkillID:      int(skillID),
		TargetCharID: int64(targetID),
		Action:       action,
	}, nil
}

func ReadChooseSkill(r *Reader) (ChooseSkillRequest, error) {
	baseID := r.I32()
	chosenID := r.I32()
	if r.Err() != nil || r.Remaining() != 0 || baseID <= 0 || chosenID < 0 {
		return ChooseSkillRequest{}, ErrMalformed
	}
	return ChooseSkillRequest{
		BaseSkillID:   int(baseID),
		ChosenSkillID: int(chosenID),
	}, nil
}

// TargetSkillListResult encodes v148 SendTargetSkillListResult (0x33, sub_9D2EAD).
func TargetSkillListResult(targetCharID int64, result int32, targetJob int32, skillIDs []int32) []byte {
	w := NewWriter(SendTargetSkillListResult)
	w.U8(1) // exclRequest -> unlock client input
	w.I32(int32(targetCharID))
	w.I32(result)
	if result == TargetSkillListSuccess {
		w.I32(targetJob)
		w.I32(int32(len(skillIDs)))
		for _, id := range skillIDs {
			w.I32(id)
		}
	}
	return w.Packet()
}

// StealSkillSuccess encodes v148 SendStealSkillResult mode 0 (0x32, sub_9D28F4).
func StealSkillSuccess(tier, slot, skillID, level, masterLevel int32) []byte {
	w := NewWriter(SendStealSkillResult)
	w.U8(1)
	w.U8(StealModeAdd)
	w.I32(tier)
	w.I32(slot)
	w.I32(skillID)
	w.I32(level)
	w.I32(masterLevel)
	return w.Packet()
}

// StealSkillDeleted encodes v148 SendStealSkillResult mode 3 (0x32, sub_9D28F4).
func StealSkillDeleted(tier, slot int32) []byte {
	w := NewWriter(SendStealSkillResult)
	w.U8(1)
	w.U8(StealModeDeleteSlot)
	w.I32(tier)
	w.I32(slot)
	return w.Packet()
}

// StealSkillFailure encodes v148 SendStealSkillResult mode 1 or 2 (0x32, sub_9D28F4).
func StealSkillFailure(mode byte) []byte {
	if mode != StealModeFailFull {
		mode = StealModeFailBad
	}
	w := NewWriter(SendStealSkillResult)
	w.U8(1)
	w.U8(mode)
	return w.Packet()
}

// ChooseSkillResult encodes v148 SendChooseSkillResult (0x9C, sub_9CC384).
func ChooseSkillResult(baseSkillID, chosenSkillID int32) []byte {
	w := NewWriter(SendChooseSkillResult)
	w.U8(1)
	if chosenSkillID > 0 {
		w.U8(1)
		w.I32(baseSkillID)
		w.I32(chosenSkillID)
	} else {
		w.U8(0)
		w.I32(baseSkillID)
	}
	return w.Packet()
}

func addPhantomStolenSkills(w *Writer, state domain.PhantomStealState) {
	for i := 0; i < domain.PhantomStolenCapacity; i++ {
		w.I32(int32(state.Stolen[i].SkillID))
	}
}

func addPhantomChosenSkills(w *Writer, state domain.PhantomStealState) {
	for i := 0; i < 4; i++ {
		w.I32(int32(state.Chosen[i]))
	}
}
