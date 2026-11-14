package persistence

import (
	"testing"

	"maple-go-server/internal/domain"
)

func TestFirstJobStatsPreserveAPAndOriginalResetOrder(t *testing.T) {
	base := domain.Character{Level: 10, STR: 42, DEX: 11, INT: 7, LUK: 6, AP: 8, SP: 2, HP: 10, MP: 2, MaxHP: 182, MaxMP: 104, BaseMaxHP: 182, BaseMaxMP: 104, VitalsModel: 1}
	for _, job := range []int{100, 200, 300, 400, 500} {
		for _, pirateNPC := range []bool{false, true} {
			if pirateNPC && job != 500 {
				continue
			}
			p := firstJobPolicy{job: job, minLevel: 10, postPirateReset: pirateNPC}
			c, err := firstJobStats(base, "first-job-server-operation", p)
			if err != nil {
				t.Fatal(err)
			}
			if c.Job != job || c.SP != 3 || c.STR+c.DEX+c.INT+c.LUK+c.AP != 74 || c.STR != 4 || c.INT != 4 || c.LUK != 4 || c.HP != c.MaxHP || c.MP != c.MaxMP {
				t.Fatalf("bad stats job=%d pirate=%v: %+v", job, pirateNPC, c)
			}
			if pirateNPC && (c.DEX != 20 || c.AP != 42) || !pirateNPC && (c.DEX != 4 || c.AP != 58) {
				t.Fatalf("reset order not conserved: %+v", c)
			}
			hp, mp := c.BaseMaxHP-base.BaseMaxHP, c.BaseMaxMP-base.BaseMaxMP
			if job == 100 && (hp < 200 || hp > 250 || mp != 0) || job == 200 && (hp != 0 || mp < 100 || mp > 150) || job >= 300 && (hp < 100 || hp > 150 || mp < 25 || mp > 50) {
				t.Fatalf("wrong bonus ranges: job=%d hp=%d mp=%d", job, hp, mp)
			}
		}
	}
}

func TestFirstJobSourceBoundRequirements(t *testing.T) {
	r := domain.JobAdvancementRequest{OperationID: "first-job-test-operation", NPCID: 1032001, Script: "npc/1032001.lua", TargetJob: 200}
	p, err := firstJobPolicyFor(r)
	if err != nil || p.minLevel != 8 {
		t.Fatalf("mage requirement: %+v %v", p, err)
	}
	r.NPCID, r.Script = 1052001, "npc/1052001.lua"
	p, err = firstJobPolicyFor(r)
	if err != nil || p.minLevel != 10 || len(p.expand) != 0 || len(p.rewards) != 0 {
		t.Fatalf("generic source invented benefits: %+v %v", p, err)
	}
	for _, target := range []int{0, 101, 210, 501, 900, 2000} {
		r.TargetJob = target
		if _, err := firstJobPolicyFor(r); err == nil {
			t.Fatalf("unreviewed target %d accepted", target)
		}
	}
	r.NPCID, r.Script, r.TargetJob = 1090000, "npc/1090000.lua", 500
	p, err = firstJobPolicyFor(r)
	if err != nil || len(p.expand) != 0 || !p.postPirateReset || len(p.rewards) != 5 {
		t.Fatalf("pirate source policy: %+v %v", p, err)
	}
	for _, reward := range p.rewards[2:] {
		if reward.id != 2330006 || reward.count != 600 || !reward.fresh {
			t.Fatal("rechargeable grant lost original stack boundaries")
		}
	}
}
