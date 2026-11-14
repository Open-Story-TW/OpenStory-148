package persistence

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestQuestJobPoliciesAreBoundToTheirSource(t *testing.T) {
	r := domain.JobAdvancementRequest{OperationID: "quest-job-test-operation", NPCID: 1090000, Script: "quest/2570.lua", TargetJob: 501}
	p, err := firstJobPolicyFor(r)
	if err != nil || p.fromJob != 0 || p.subJob != domain.SubJobCannoneer || len(p.expand) != 3 || p.quests[2570] != 1 || len(p.complete) != 1 || p.rewards[0].id != 1532000 {
		t.Fatalf("cannoneer policy: %+v %v", p, err)
	}
	for _, bad := range []domain.JobAdvancementRequest{
		{OperationID: r.OperationID, NPCID: 1090000, Script: "quest/2570.lua", TargetJob: 500},
		{OperationID: r.OperationID, NPCID: 1090000, Script: "npc/1090000.lua", TargetJob: 501},
		{OperationID: r.OperationID, NPCID: 1101003, Script: "quest/20102.lua", TargetJob: 1200},
		{OperationID: r.OperationID, NPCID: 1101004, Script: "quest/20102.lua", TargetJob: 1100},
		{OperationID: r.OperationID, NPCID: 2151001, Script: "quest/23011.lua", TargetJob: 3300},
	} {
		if _, err := firstJobPolicyFor(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// The pirate instructor's own script keeps its policy.
	if p, err = firstJobPolicyFor(domain.JobAdvancementRequest{OperationID: r.OperationID, NPCID: 1090000, Script: "npc/1090000.lua", TargetJob: 500}); err != nil || !p.postPirateReset {
		t.Fatalf("pirate policy: %+v %v", p, err)
	}
	aran, err := firstJobPolicyFor(domain.JobAdvancementRequest{OperationID: r.OperationID, NPCID: 1201001, Script: "quest/21101.lua", TargetJob: 2100})
	if err != nil || aran.fromJob != 2000 || aran.base != [4]int{35, 4, 4, 4} || aran.quests[21100] != 2 || aran.quests[21101] != 0 || len(aran.complete) != 2 {
		t.Fatalf("aran policy: %+v %v", aran, err)
	}
}

func TestQuestJobStatsFollowTheFamily(t *testing.T) {
	base := domain.Character{Level: 10, STR: 12, DEX: 5, INT: 4, LUK: 4, AP: 45, SP: 0, HP: 50, MP: 5, MaxHP: 50, MaxMP: 5, BaseMaxHP: 50, BaseMaxMP: 5, VitalsModel: 1}
	for _, tc := range []struct {
		from, to        int
		base            [4]int
		hp, mp, sp      bool
		spGain, wantSTR int
	}{
		{0, 501, [4]int{}, true, true, false, 1, 4},
		{1000, 1100, [4]int{}, true, false, false, 1, 4},
		{1000, 1200, [4]int{}, false, true, false, 1, 4},
		{1000, 1400, [4]int{}, true, true, false, 1, 4},
		{2000, 2100, [4]int{35, 4, 4, 4}, true, false, false, 1, 35},
		{3000, 3200, [4]int{}, true, false, true, 5, 4},
		{3000, 3500, [4]int{}, true, true, true, 5, 4},
	} {
		c := base
		c.Job = tc.from
		got, err := firstJobStats(c, "quest-job-stats-operation", firstJobPolicy{job: tc.to, fromJob: tc.from, minLevel: 10, base: tc.base})
		if err != nil {
			t.Fatalf("%d->%d: %v", tc.from, tc.to, err)
		}
		if got.Job != tc.to || got.SP != tc.spGain || got.STR != tc.wantSTR || got.STR+got.DEX+got.INT+got.LUK+got.AP != 70 {
			t.Fatalf("%d->%d stats: %+v", tc.from, tc.to, got)
		}
		hp, mp := got.BaseMaxHP-base.BaseMaxHP, got.BaseMaxMP-base.BaseMaxMP
		if (hp > 0) != tc.hp || (mp > 0) != tc.mp {
			t.Fatalf("%d->%d gains hp=%d mp=%d", tc.from, tc.to, hp, mp)
		}
		// The advancement starts only from its own beginner job.
		c.Job = 0
		if tc.from != 0 {
			if _, err = firstJobStats(c, "quest-job-stats-operation", firstJobPolicy{job: tc.to, fromJob: tc.from, minLevel: 10}); err == nil {
				t.Fatalf("%d->%d accepted job 0", tc.from, tc.to)
			}
		}
	}
}

func TestDualBladeQuestJobsKeepStatsAndCheckTheirQuest(t *testing.T) {
	op := "dual-blade-quest-job-operation"
	for _, tc := range []struct {
		script         string
		from, to, lvl  int
		consume, gives int
		verify         int
	}{
		{"quest/2623.lua", 400, 430, 20, 4032616, 1342000, 2623},
		{"quest/2638.lua", 430, 431, 30, 4031013, 0, 2638},
		{"quest/2642.lua", 431, 432, 55, 0, 1132021, 2642},
	} {
		p, err := firstJobPolicyFor(domain.JobAdvancementRequest{OperationID: op, NPCID: 1056000, Script: tc.script, TargetJob: tc.to})
		if err != nil || p.fromJob != tc.from || p.minLevel != tc.lvl || p.subJob != domain.SubJobDualBlade || p.verify != tc.verify || p.quests[tc.verify] != 1 || len(p.complete) != 1 || p.complete[0] != tc.verify {
			t.Fatalf("%s policy: %+v %v", tc.script, p, err)
		}
		if (tc.consume != 0) != (len(p.consume) == 1) || tc.consume != 0 && p.consume[0].id != tc.consume {
			t.Fatalf("%s consumes %+v", tc.script, p.consume)
		}
		if (tc.gives != 0) != (len(p.rewards) == 1) || tc.gives != 0 && p.rewards[0].id != tc.gives {
			t.Fatalf("%s rewards %+v", tc.script, p.rewards)
		}
		// Another NPC, source or target is refused.
		for _, bad := range []domain.JobAdvancementRequest{
			{OperationID: op, NPCID: 1057001, Script: tc.script, TargetJob: tc.to},
			{OperationID: op, NPCID: 1056000, Script: tc.script, TargetJob: tc.to + 1},
			{OperationID: op, NPCID: 1056000, Script: "npc/1056000.lua", TargetJob: tc.to},
		} {
			if _, err := firstJobPolicyFor(bad); err == nil {
				t.Fatalf("accepted %+v", bad)
			}
		}
		c := domain.Character{Job: tc.from, Level: tc.lvl, STR: 4, DEX: 25, INT: 4, LUK: 60, AP: 3, SP: 2, HP: 500, MP: 200, MaxHP: 500, MaxMP: 200, BaseMaxHP: 500, BaseMaxMP: 200, VitalsModel: 1}
		got, err := firstJobStats(c, op, p)
		if err != nil {
			t.Fatalf("%s stats: %v", tc.script, err)
		}
		if got.Job != tc.to || got.STR != 4 || got.DEX != 25 || got.LUK != 60 || got.AP != 3 || got.SP != 3 {
			t.Fatalf("%s reset stats: %+v", tc.script, got)
		}
		hp := got.BaseMaxHP - c.BaseMaxHP
		if tc.to == 430 && (hp < 350 || hp > 450) || tc.to != 430 && hp != 0 || got.BaseMaxMP != c.BaseMaxMP {
			t.Fatalf("%s gains hp=%d mp=%d", tc.script, hp, got.BaseMaxMP-c.BaseMaxMP)
		}
		c.Level = tc.lvl - 1
		if _, err = firstJobStats(c, op, p); err == nil {
			t.Fatalf("%s accepted level %d", tc.script, c.Level)
		}
	}
}
