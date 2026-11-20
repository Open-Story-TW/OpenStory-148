package world

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

type fakePhantomStealRepo struct {
	domain.Repository
	saved []domain.PhantomStealState
}

func (r *fakePhantomStealRepo) SavePhantomStealState(_ context.Context, session domain.CharacterSession, state domain.PhantomStealState) (domain.Character, error) {
	r.saved = append(r.saved, state)
	c := session.Character
	c.PhantomSteal = state
	return c, nil
}

func TestPhantomStealInspectStealChooseDeleteAndCastLevel(t *testing.T) {
	repo := &fakePhantomStealRepo{}
	s := &Server{
		repo:     repo,
		gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{}},
	}
	phantom := &liveCharacter{
		session: domain.CharacterSession{
			CharacterID: 10,
			Character: domain.Character{
				ID: 10, Job: 2412, Level: 200, MapID: 180000001, Revision: 1,
				Skills: []domain.SkillState{
					{ID: 24001001, Level: 12},
					{ID: 24101001, Level: 20},
					{ID: 24111001, Level: 20},
					{ID: 24121001, Level: 30},
				},
			},
		},
	}
	donor := &liveCharacter{
		session: domain.CharacterSession{
			CharacterID: 20,
			Character: domain.Character{
				ID: 20, Job: 112, Level: 200, MapID: 180000001, Revision: 1,
				Skills: []domain.SkillState{
					{ID: 1001003, Level: 15, MasterLevel: 20}, // Iron Body (tier 1, stealable)
					{ID: 1100000, Level: 20},                  // Sword Mastery (passive, NOT stealable)
					{ID: 1101006, Level: 20},                  // Rage (tier 2, stealable)
					{ID: 1121002, Level: 30, MasterLevel: 30}, // Power Stance (tier 4, stealable)
					{ID: 1121008, Level: 30, MasterLevel: 30}, // Raging Blow (tier 4, stealable)
					{ID: 1121000, Level: 30, MasterLevel: 30}, // Maple Warrior (tier 4, stealable)
					{ID: 1121010, Level: 30},                  // Enrage (excluded by sub_4E5A63)
				},
			},
		},
	}
	pSink := &gameplayTestSink{}
	dSink := &gameplayTestSink{}
	room := &gameplayRoom{
		members: map[int64]*gameplayMember{
			10: {live: phantom, sink: pSink},
			20: {live: donor, sink: dSink},
		},
	}
	s.gameplay.rooms[mapKey{channel: 1, mapID: 180000001}] = room

	// 1) 0x109 Inspect peer
	bInspect := make([]byte, 4)
	binary.LittleEndian.PutUint32(bInspect, 20)
	if err := s.handlePhantomSteal(context.Background(), 1, phantom, pSink, protocol.RecvTargetSkillList, bInspect); err != nil {
		t.Fatal(err)
	}
	if len(pSink.packets) != 1 {
		t.Fatalf("expected 1 inspect packet, got %d", len(pSink.packets))
	}
	r := protocol.NewReader(pSink.packets[0])
	if r.U16() != protocol.SendTargetSkillListResult || r.U8() != 1 || r.I32() != 20 || r.I32() != protocol.TargetSkillListSuccess || r.I32() != 112 {
		t.Fatalf("bad inspect header: %x", pSink.packets[0])
	}
	cnt := int(r.I32())
	gotSkills := make([]int32, cnt)
	for i := 0; i < cnt; i++ {
		gotSkills[i] = r.I32()
	}
	wantSkills := []int32{1001003, 1101006, 1121000, 1121002, 1121008}
	if len(gotSkills) != len(wantSkills) {
		t.Fatalf("inspect skills=%v, want %v", gotSkills, wantSkills)
	}
	for i := range wantSkills {
		if gotSkills[i] != wantSkills[i] {
			t.Fatalf("inspect skill[%d]=%d, want %d", i, gotSkills[i], wantSkills[i])
		}
	}

	// 2) 0x108 Steal 1001003 (tier 1 slot 0), 1121002 (tier 4 slot 0), 1121008 (tier 4 slot 1), and verify 3rd tier-4 fails (full)
	steal := func(sid int, action byte) []byte {
		b := make([]byte, 9)
		binary.LittleEndian.PutUint32(b[0:4], uint32(sid))
		binary.LittleEndian.PutUint32(b[4:8], 20)
		b[8] = action
		before := len(pSink.packets)
		if err := s.handlePhantomSteal(context.Background(), 1, phantom, pSink, protocol.RecvStealSkill, b); err != nil {
			t.Fatal(err)
		}
		return pSink.packets[before]
	}
	p1 := steal(1001003, 0)
	r = protocol.NewReader(p1)
	if r.U16() != protocol.SendStealSkillResult || r.U8() != 1 || r.U8() != protocol.StealModeAdd || r.I32() != 1 || r.I32() != 0 || r.I32() != 1001003 || r.I32() != 15 || r.I32() != 20 {
		t.Fatalf("bad steal 1001003 reply: %x", p1)
	}
	p2 := steal(1121002, 0)
	r = protocol.NewReader(p2)
	if r.U16() != protocol.SendStealSkillResult || r.U8() != 1 || r.U8() != protocol.StealModeAdd || r.I32() != 4 || r.I32() != 0 || r.I32() != 1121002 {
		t.Fatalf("bad steal 1121002 reply: %x", p2)
	}
	_ = steal(1121008, 0)
	pFull := steal(1121000, 0) // Tier 4 capacity is 2 -> must fail with StealModeFailFull (1)
	r = protocol.NewReader(pFull)
	if r.U16() != protocol.SendStealSkillResult || r.U8() != 1 || r.U8() != protocol.StealModeFailFull {
		t.Fatalf("expected tier-4 full failure, got %x", pFull)
	}

	// 3) 0x107 Choose 1001003 into 24001001; reject tier-4 skill in tier-1 slot
	choose := func(baseID, chosenID int) []byte {
		b := make([]byte, 8)
		binary.LittleEndian.PutUint32(b[0:4], uint32(baseID))
		binary.LittleEndian.PutUint32(b[4:8], uint32(chosenID))
		before := len(pSink.packets)
		if err := s.handlePhantomSteal(context.Background(), 1, phantom, pSink, protocol.RecvChooseSkill, b); err != nil {
			t.Fatal(err)
		}
		return pSink.packets[before]
	}
	pCh := choose(24001001, 1001003)
	r = protocol.NewReader(pCh)
	if r.U16() != protocol.SendChooseSkillResult || r.U8() != 1 || r.U8() != 1 || r.I32() != 24001001 || r.I32() != 1001003 {
		t.Fatalf("bad choose reply: %x", pCh)
	}
	pReject := choose(24001001, 1121002)
	if binary.LittleEndian.Uint16(pReject[:2]) != protocol.SendUpdateStats {
		t.Fatalf("expected EnableActions on tier violation, got %x", pReject)
	}
	if phantom.session.Character.PhantomSteal.Chosen[0] != 1001003 {
		t.Fatalf("tier violation mutated chosen slot: %+v", phantom.session.Character.PhantomSteal.Chosen)
	}

	// 4) Effective cast/attack level for chosen stolen skill is min(stolen=15, base 24001001=12) = 12
	now := time.Unix(1000, 0)
	if lv, ok := skills.CastLevel(phantom.session.Character, 1001003, 1001003, 12, now); !ok || lv != 12 {
		t.Fatalf("CastLevel for chosen stolen skill: lv=%d ok=%v, want 12/true", lv, ok)
	}
	if lv := attackSkillLevel(phantom.session.Character, 1001003, now); lv != 12 {
		t.Fatalf("attackSkillLevel for chosen stolen skill: %d, want 12", lv)
	}

	// 5) 0x108 Delete 1001003 clears both stolen slot and chosen binding
	pDel := steal(1001003, 1)
	r = protocol.NewReader(pDel)
	if r.U16() != protocol.SendStealSkillResult || r.U8() != 1 || r.U8() != protocol.StealModeDeleteSlot || r.I32() != 1 || r.I32() != 0 {
		t.Fatalf("bad delete reply: %x", pDel)
	}
	if phantom.session.Character.PhantomSteal.Chosen[0] != 0 || phantom.session.Character.ChosenStolenLevel(1001003, now) != 0 {
		t.Fatalf("delete did not clear chosen binding: %+v", phantom.session.Character.PhantomSteal)
	}
	if len(repo.saved) != 5 {
		t.Fatalf("expected 5 persisted transitions, got %d", len(repo.saved))
	}
}
