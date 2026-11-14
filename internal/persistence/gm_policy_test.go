package persistence

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func gmPolicyCharacter() domain.Character {
	return domain.Character{ID: 1, AccountID: 2, WorldID: 3, Level: 10, EXP: 27, Job: 0, STR: 57, DEX: 4, INT: 4, LUK: 4, HP: 20, MP: 5, MaxHP: 180, MaxMP: 90, BaseMaxHP: 180, BaseMaxMP: 90, VitalsModel: 1, SP: 1, MapID: 10000}
}

func TestQAGMLevelSameTargetPreservesEarnedExperience(t *testing.T) {
	c := gmPolicyCharacter()
	before := c
	changed, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "level", Args: []int64{10}, OperationID: "qa-level-same-target"}, time.Now())
	if err != nil || changed || !reflect.DeepEqual(c, before) {
		t.Fatalf("same target reset state: %+v %v", c, err)
	}
}

func TestQAGMPolicyUsesReviewedGrowthAndFirstJob(t *testing.T) {
	c := gmPolicyCharacter()
	c.Level, c.EXP = 9, 12
	changed, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "level", Args: []int64{10}, OperationID: "qa-progressive-level"}, time.Now())
	if err != nil || !changed || c.Level != 10 || c.EXP != 0 || c.HP != c.MaxHP || c.MaxHP <= 180 {
		t.Fatalf("growth %+v %v", c, err)
	}
	beforeAP := c.AP + c.STR + c.DEX + c.INT + c.LUK
	changed, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "job", Args: []int64{100}, OperationID: "qa-first-job-change"}, time.Now())
	if err != nil || !changed || c.Job != 100 || c.AP+c.STR+c.DEX+c.INT+c.LUK != beforeAP || c.STR != 4 || c.SP != 2 || len(c.Equipment) != 0 || c.InventorySlots[0] != 0 {
		t.Fatalf("first job %+v %v", c, err)
	}
	before := c
	changed, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "job", Args: []int64{100}}, time.Now())
	if err != nil || changed || !reflect.DeepEqual(before, c) {
		t.Fatal("same job repeated grants")
	}
}

func TestQAGMPolicyRejectsUnreviewedJobsAndSkills(t *testing.T) {
	for _, req := range []domain.GMCommandRequest{
		{Name: "job", Args: []int64{900}}, {Name: "job", Args: []int64{4294967396}},
		{Name: "level", Args: []int64{9}}, {Name: "skill", Args: []int64{9001000, 1}},
		{Name: "skill", Args: []int64{1001, 4}}, {Name: "unknown"},
	} {
		c := gmPolicyCharacter()
		before := c
		if _, _, _, err := applyQAGMPolicy(&c, req, time.Now()); err == nil || !reflect.DeepEqual(before, c) {
			t.Fatalf("accepted/changed invalid %+v %+v %v", req, c, err)
		}
	}
	c := gmPolicyCharacter()
	c.Job = 900
	if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "heal"}, time.Now()); err == nil {
		t.Fatal("unreviewed job exposed")
	}
}

func TestQAGMSkillExtensionDoesNotSpendSPOrCast(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	c := gmPolicyCharacter()
	beforeSP, beforeMP := c.SP, c.MP
	changed, id, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{1002, 3}}, time.Now())
	if err != nil || !changed || id != 1002 || learned(c, id).Level != 3 || c.SP != beforeSP || c.MP != beforeMP || len(c.SkillBuffs) != 0 || len(c.SkillCooldowns) != 0 {
		t.Fatalf("skill extension %+v %v", c, err)
	}
	old := append([]domain.SkillState(nil), c.Skills...)
	c.SkillBuffs = []domain.SkillBuff{{ID: 1001, ExpiresAt: time.Now().Add(time.Minute)}}
	c.Skills = append(c.Skills, domain.SkillState{ID: 1001, Level: 1})
	if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{1001, 2}}, time.Now()); err == nil || c.Skills[0] != old[0] || c.Skills[1].Level != 1 {
		t.Fatal("active skill changed")
	}
}

func TestQAGMGrantMustMatchFreshRankAndEveryScope(t *testing.T) {
	now := time.Now()
	c := gmPolicyCharacter()
	c.GM = 6
	g := domain.QAGMGrant{AccountID: c.AccountID, CharacterID: c.ID, WorldID: c.WorldID, Role: domain.QAGMRole, Enabled: true, ExpiresAt: now.Add(time.Minute)}
	if !validQAGMGrant(g, c, now) {
		t.Fatal("valid grant rejected")
	}
	for _, modify := range []func(*domain.QAGMGrant){func(g *domain.QAGMGrant) { g.AccountID++ }, func(g *domain.QAGMGrant) { g.CharacterID++ }, func(g *domain.QAGMGrant) { g.WorldID++ }, func(g *domain.QAGMGrant) { g.Enabled = false }, func(g *domain.QAGMGrant) { g.Role = "gm" }, func(g *domain.QAGMGrant) { g.ExpiresAt = now }} {
		x := g
		modify(&x)
		if validQAGMGrant(x, c, now) {
			t.Fatal("mismatched grant accepted")
		}
	}
	c.GM = 0
	if validQAGMGrant(g, c, now) {
		t.Fatal("role bypassed fresh rank")
	}
}

func TestQAGMWarpAuthorizesOnlyAllowlistedFieldsWithoutMoving(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("allowlisted fields load from MAPLEGO_WZ_ROOT")
	}
	c := gmPolicyCharacter()
	before := c
	changed, _, mapChanged, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "warp", Args: []int64{101010100}}, time.Now())
	if err != nil || changed || !mapChanged || !reflect.DeepEqual(before, c) {
		t.Fatalf("allowlisted destination %+v %v", c, err)
	}
	for _, mapID := range []int64{20000, 999999998, 910000000} {
		if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "warp", Args: []int64{mapID}}, time.Now()); err == nil || !reflect.DeepEqual(before, c) {
			t.Fatalf("non-allowlisted destination %d accepted", mapID)
		}
	}
	same := c
	same.MapID = 101010100
	if _, _, mapChanged, err = applyQAGMPolicy(&same, domain.GMCommandRequest{Name: "warp", Args: []int64{101010100}}, time.Now()); err != nil || !mapChanged {
		t.Fatal("same-map warp must return to the spawn point", err)
	}
	for mapID := range domain.QAWarpMaps {
		if _, _, mapChanged, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "warp", Args: []int64{int64(mapID)}}, time.Now()); err != nil || !mapChanged {
			t.Fatalf("allowlisted %d has no WZ spawn: %v", mapID, err)
		}
	}
}

func TestQAGMJobReachesEveryFamilyWithSubJobAndSPBooks(t *testing.T) {
	c := gmPolicyCharacter()
	c.Level, c.SP = 30, 5
	for _, tc := range []struct{ job, sub, sp int }{{2210, 0, 0}, {2211, 0, 0}, {431, 1, 0}, {532, 2, 0}, {3112, 0, 0}, {2412, 0, 0}, {1511, 0, 0}} {
		changed, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "job", Args: []int64{int64(tc.job)}, OperationID: fmt.Sprintf("qa-any-job-%d", tc.job)}, time.Now())
		if err != nil || !changed || c.Job != tc.job || c.SubJob != tc.sub {
			t.Fatalf("job %d: %+v %v", tc.job, c, err)
		}
		if tc.job == 2210 && c.SP != 0 || tc.job == 2211 && (c.SPBooks[0] != 5 || c.SPTable()[0] != 5) {
			t.Fatalf("evan SP books after %d: sp=%d books=%v", tc.job, c.SP, c.SPBooks)
		}
	}
	for _, job := range []int64{800, 900, 2219, 3400, 600} {
		before := c
		if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "job", Args: []int64{job}}, time.Now()); err == nil || !reflect.DeepEqual(before, c) {
			t.Fatalf("nonexistent job %d accepted", job)
		}
	}
}

func TestQAGMSkillGrantsInvisibleAndMasterLevelSkills(t *testing.T) {
	if os.Getenv("MAPLEGO_WZ_ROOT") == "" {
		t.Skip("fourth-job skill templates load from MAPLEGO_WZ_ROOT")
	}
	c := gmPolicyCharacter()
	c.Job, c.Level = 512, 120
	// 楓葉祝福 5121000: invisible, masterLevel-gated (skill book in play).
	changed, id, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{5121000, 20}}, time.Now())
	if err != nil || !changed || id != 5121000 {
		t.Fatalf("maple warrior grant: %v", err)
	}
	if s := learned(c, 5121000); s.Level != 20 {
		t.Fatalf("maple warrior state %+v", s)
	}
	// 5121001: WZ masterLevel 10 means a skill book gates levels in play.
	if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{5121001, 25}}, time.Now()); err != nil {
		t.Fatalf("master-level skill grant: %v", err)
	}
	if s := learned(c, 5121001); s.Level != 25 || s.MasterLevel != 25 {
		t.Fatalf("master-level state %+v", s)
	}
	if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{5121000, 31}}, time.Now()); err == nil {
		t.Fatal("level above WZ max granted")
	}
	c.Job = 511
	if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{5221000, 10}}, time.Now()); err == nil {
		t.Fatal("other branch's fourth-job skill granted")
	}
}

func TestQAGMLevelPastSecondJobForEveryBranch(t *testing.T) {
	for _, job := range []int{110, 130, 210, 310, 410, 430, 510, 530, 1110, 2110, 2211, 3110, 3210} {
		c := gmPolicyCharacter()
		c.Job, c.Level, c.EXP = job, 30, 0
		if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "level", Args: []int64{70}, OperationID: fmt.Sprintf("qa-level-70-%d", job)}, time.Now()); err != nil || c.Level != 70 {
			t.Fatalf("job %d level 30->70: level=%d %v", job, c.Level, err)
		}
	}
}

func TestQAGMStatsMeetWeaponRequirementsAndJobClampsVitals(t *testing.T) {
	c := gmPolicyCharacter()
	c.Job, c.Level = 3212, 120
	if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "stats", Args: []int64{4, 4, 400, 60}}, time.Now()); err != nil || c.INT != 400 || c.LUK != 60 || c.STR != 4 {
		t.Fatalf("stats %+v %v", c, err)
	}
	if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "stats", Args: []int64{3, 4, 4, 4}}, time.Now()); err == nil || !strings.Contains(err.Error(), "abilities must be 4..") {
		t.Fatalf("ability below 4 accepted: %v", err)
	}
	v := gmPolicyCharacter()
	v.Job, v.Level, v.HP, v.MaxHP, v.BaseMaxHP = 1210, 70, 2054, 2054, 2054
	v.VitalsModel = 0 // aggregate model: MaxHP stays, HP must still fit it
	v.MaxHP = 1713
	if _, _, _, err := applyQAGMPolicy(&v, domain.GMCommandRequest{Name: "job", Args: []int64{1211}, OperationID: "qa-clamp-job-1211"}, time.Now()); err != nil || v.HP != 1713 {
		t.Fatalf("job change left HP above max: %d/%d %v", v.HP, v.MaxHP, err)
	}
}

func TestQAGMSkillRefusalCarriesTheCause(t *testing.T) {
	if os.Getenv("MAPLEGO_WZ_ROOT") == "" {
		t.Skip("skill templates load from MAPLEGO_WZ_ROOT")
	}
	c := gmPolicyCharacter()
	c.Job, c.Level = 320, 70
	_, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{3201005, 99}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "max level") && !strings.Contains(err.Error(), "registered") {
		t.Fatalf("over-max refusal without cause: %v", err)
	}
}

func TestQAGMMobHoldOnlyOnQAFields(t *testing.T) {
	c := gmPolicyCharacter()
	c.MapID = 104010000
	before := c
	for _, arg := range []int64{0, 1} {
		if changed, _, mapChanged, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "mobhold", Args: []int64{arg}}, time.Now()); err != nil || changed || mapChanged || !reflect.DeepEqual(before, c) {
			t.Fatalf("mobhold %d on a QA field: %v", arg, err)
		}
	}
	if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "mobhold", Args: []int64{2}}, time.Now()); err == nil {
		t.Fatal("mobhold 2 accepted")
	}
	c.MapID = 910000000
	if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "mobhold", Args: []int64{1}}, time.Now()); err == nil {
		t.Fatal("mobhold outside QA fields accepted")
	}
}

func TestQAGMHPSetsZeroToMaxHP(t *testing.T) {
	c := gmPolicyCharacter()
	c.HP = c.MaxHP
	for _, v := range []int64{1, int64(c.MaxHP) - 1, 0} {
		next := c
		changed, _, _, err := applyQAGMPolicy(&next, domain.GMCommandRequest{Name: "hp", Args: []int64{v}}, time.Now())
		if err != nil || !changed || next.HP != int(v) {
			t.Fatalf("hp %d: %d %v %v", v, next.HP, changed, err)
		}
	}
	for _, v := range []int64{-1, int64(c.MaxHP) + 1} {
		next := c
		if _, _, _, err := applyQAGMPolicy(&next, domain.GMCommandRequest{Name: "hp", Args: []int64{v}}, time.Now()); err == nil {
			t.Fatalf("hp %d accepted", v)
		}
	}
	dead := c
	dead.HP = 0
	if _, _, _, err := applyQAGMPolicy(&dead, domain.GMCommandRequest{Name: "hp", Args: []int64{10}}, time.Now()); err == nil {
		t.Fatal("dead character revived by !hp")
	}
}
