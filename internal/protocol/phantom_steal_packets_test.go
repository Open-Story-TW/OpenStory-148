package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestPhantomStealPacketsWireFormat(t *testing.T) {
	// 0x109 RecvTargetSkillList
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, 777)
	tid, err := ReadTargetSkillList(NewReader(buf))
	if err != nil || tid != 777 {
		t.Fatalf("ReadTargetSkillList: tid=%d err=%v", tid, err)
	}

	// 0x108 RecvStealSkill
	bSteal := make([]byte, 9)
	binary.LittleEndian.PutUint32(bSteal[0:4], 1001003)
	binary.LittleEndian.PutUint32(bSteal[4:8], 777)
	bSteal[8] = 0
	req, err := ReadStealSkill(NewReader(bSteal))
	if err != nil || req.SkillID != 1001003 || req.TargetCharID != 777 || req.Action != 0 {
		t.Fatalf("ReadStealSkill: %+v err=%v", req, err)
	}

	// 0x107 RecvChooseSkill
	bChoose := make([]byte, 8)
	binary.LittleEndian.PutUint32(bChoose[0:4], 24001001)
	binary.LittleEndian.PutUint32(bChoose[4:8], 1001003)
	creq, err := ReadChooseSkill(NewReader(bChoose))
	if err != nil || creq.BaseSkillID != 24001001 || creq.ChosenSkillID != 1001003 {
		t.Fatalf("ReadChooseSkill: %+v err=%v", creq, err)
	}

	// 0x33 SendTargetSkillListResult
	pList := TargetSkillListResult(777, TargetSkillListSuccess, 112, []int32{1001003, 1101006})
	r := NewReader(pList)
	if r.U16() != SendTargetSkillListResult || r.U8() != 1 || r.I32() != 777 || r.I32() != 4 || r.I32() != 112 || r.I32() != 2 || r.I32() != 1001003 || r.I32() != 1101006 || r.Remaining() != 0 {
		t.Fatalf("TargetSkillListResult wire mismatch: %x", pList)
	}

	// 0x32 SendStealSkillResult (add + delete + fail)
	pAdd := StealSkillSuccess(1, 0, 1001003, 15, 20)
	r = NewReader(pAdd)
	if r.U16() != SendStealSkillResult || r.U8() != 1 || r.U8() != 0 || r.I32() != 1 || r.I32() != 0 || r.I32() != 1001003 || r.I32() != 15 || r.I32() != 20 || r.Remaining() != 0 {
		t.Fatalf("StealSkillSuccess wire mismatch: %x", pAdd)
	}
	pDel := StealSkillDeleted(1, 0)
	r = NewReader(pDel)
	if r.U16() != SendStealSkillResult || r.U8() != 1 || r.U8() != 3 || r.I32() != 1 || r.I32() != 0 || r.Remaining() != 0 {
		t.Fatalf("StealSkillDeleted wire mismatch: %x", pDel)
	}
	pFail := StealSkillFailure(StealModeFailFull)
	r = NewReader(pFail)
	if r.U16() != SendStealSkillResult || r.U8() != 1 || r.U8() != 1 || r.Remaining() != 0 {
		t.Fatalf("StealSkillFailure wire mismatch: %x", pFail)
	}

	// 0x9C SendChooseSkillResult (equip + unequip)
	pEq := ChooseSkillResult(24001001, 1001003)
	r = NewReader(pEq)
	if r.U16() != SendChooseSkillResult || r.U8() != 1 || r.U8() != 1 || r.I32() != 24001001 || r.I32() != 1001003 || r.Remaining() != 0 {
		t.Fatalf("ChooseSkillResult equip wire mismatch: %x", pEq)
	}
	pUn := ChooseSkillResult(24001001, 0)
	r = NewReader(pUn)
	if r.U16() != SendChooseSkillResult || r.U8() != 1 || r.U8() != 0 || r.I32() != 24001001 || r.Remaining() != 0 {
		t.Fatalf("ChooseSkillResult unequip wire mismatch: %x", pUn)
	}
}

func TestAddCharacterInfoEncodesPhantomStealAndChosenSlots(t *testing.T) {
	c := domain.Character{
		ID: 55, Name: "PhantomQA", Job: 2412, Level: 200, MapID: 180000001,
		Skills: []domain.SkillState{{ID: 24001001, Level: 20}},
	}
	c.PhantomSteal.Stolen[0] = domain.PhantomStolenSkill{Tier: 1, Slot: 0, SkillID: 1001003, Level: 15, MasterLevel: 20}
	c.PhantomSteal.Stolen[11] = domain.PhantomStolenSkill{Tier: 4, Slot: 0, SkillID: 1121002, Level: 30, MasterLevel: 30}
	c.PhantomSteal.Chosen[0] = 1001003
	c.PhantomSteal.Chosen[3] = 1121002

	w := NewWriter(0x00)
	addCharacterInfo(w, c, time.Unix(1000, 0))
	raw := w.Packet()

	// Verify stolen slot 0 + slot 11 and chosen slot 0 + slot 3 appear consecutively
	// in the mask 0x20000000 + 0x10000000 tail block.
	wantBlock := make([]byte, (13+4)*4)
	binary.LittleEndian.PutUint32(wantBlock[0:4], 1001003)
	binary.LittleEndian.PutUint32(wantBlock[11*4:12*4], 1121002)
	binary.LittleEndian.PutUint32(wantBlock[13*4:14*4], 1001003)
	binary.LittleEndian.PutUint32(wantBlock[16*4:17*4], 1121002)
	if !bytes.Contains(raw, wantBlock) {
		t.Fatalf("AddCharacterInfo missing expected 13-stolen + 4-chosen Phantom block")
	}
	// Stolen skill records must also appear in wireSkills so the client knows their level/master level.
	ws := wireSkills(c)
	foundIronBody, foundStance := false, false
	for _, s := range ws {
		if s.ID == 1001003 && s.Level == 15 && s.MasterLevel == 20 {
			foundIronBody = true
		}
		if s.ID == 1121002 && s.Level == 30 && s.MasterLevel == 30 {
			foundStance = true
		}
	}
	if !foundIronBody || !foundStance {
		t.Fatalf("wireSkills missing stolen skill records: %+v", ws)
	}
}
