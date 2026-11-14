package main

import (
	"strings"
	"testing"
)

func TestKindOf(t *testing.T) {
	for in, want := range map[string]string{
		"被動": "passive", "被動／Buff": "passive", "Buff": "buff", "Buff／組隊Buff": "party_buff",
		"組隊Buff／恢復／治療": "party_buff", "Buff／召喚": "summon", "攻擊／怪物狀態": "attack", "特殊": "", "怪物狀態": "",
	} {
		if got := kindOf(in); got != want {
			t.Errorf("kindOf(%q)=%q want %q", in, got, want)
		}
	}
}

func TestBuildReadsWZExpectations(t *testing.T) {
	csv := "\ufeff技能ID,技能名稱,職業代碼,類型,WZ啟用,Go已註冊,測試狀態\n" +
		"2121004,魔力無限,212,Buff,1,1,x\n" +
		"2121003,地獄爆發,212,攻擊,1,1,x\n" +
		"2121008,特殊技,212,特殊,1,1,x\n" +
		"2121001,停用,212,攻擊,0,1,x\n"
	rows, header, err := readCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	out := build("", rows, header, 200, nil, nil, nil)
	if len(out.Cases) != 2 || len(out.Skipped) != 1 {
		t.Fatalf("cases %+v skipped %+v", out.Cases, out.Skipped)
	}
	buff, attack := out.Cases[0], out.Cases[1]
	if buff.Kind != "buff" || buff.SkillLevel != 30 || buff.ExpectDurationS != 40 {
		t.Errorf("buff %+v", buff)
	}
	if attack.Kind != "attack" || attack.MaxTargets != 12 || attack.Lines != 5 || attack.ExtraSkills["2111003"] == 0 {
		t.Errorf("attack %+v", attack)
	}
}
