package persistence

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"reflect"
	"testing"
	"time"
)

func TestSpearmanAdvancementPreservesStatsAndRecomputesBonusAfterPassive(t *testing.T) {
	c := domain.Character{Job: 100, Level: 30, STR: 100, DEX: 30, INT: 4, LUK: 4, AP: 7, SP: 2, VitalsModel: 1, BaseMaxHP: 500, BaseMaxMP: 100, MaxHP: 624, MaxMP: 107, HP: 100, MP: 5, Equipment: []domain.Equipment{{Slot: -5, HP: 20, MP: 7}}, Skills: []domain.SkillState{{ID: 1000006, Level: 10}, {ID: 1001004, Level: 1}}}
	next, err := spearmanAdvancementStats(c, "spearman-reviewed-policy")
	if err != nil || next.Job != 130 || next.BaseMaxHP < 800 || next.BaseMaxHP > 850 || next.BaseMaxMP != 100 || next.MaxHP != (next.BaseMaxHP+20)*120/100 || next.MaxMP != 107 || next.HP != next.MaxHP || next.MP != next.MaxMP || next.SP != 3 || next.AP != c.AP || next.STR != c.STR || next.DEX != c.DEX || next.INT != c.INT || next.LUK != c.LUK || !reflect.DeepEqual(next.Skills, c.Skills) || !reflect.DeepEqual(next.Equipment, c.Equipment) {
		t.Fatalf("spearman %+v %v", next, err)
	}
	repeated, e := spearmanAdvancementStats(c, "spearman-reviewed-policy")
	if e != nil || !reflect.DeepEqual(next, repeated) {
		t.Fatal("same receipt rerolled intrinsic bonus")
	}
	for _, change := range []func(*domain.Character){func(c *domain.Character) { c.Job = 0 }, func(c *domain.Character) { c.Job = 110 }, func(c *domain.Character) { c.Level = 29 }, func(c *domain.Character) { c.HP = 0 }, func(c *domain.Character) { c.VitalsModel = 0 }} {
		bad := c
		change(&bad)
		before := bad
		if _, err := spearmanAdvancementStats(bad, "spearman-invalid-policy"); err == nil || !reflect.DeepEqual(bad, before) {
			t.Fatal("invalid advancement", bad, err)
		}
	}
	beforeHP, beforeMP := next.BaseMaxHP, next.BaseMaxMP
	if err = applyQuestExperience(&next, content.ExperienceForLevel(30), "spearman-normal-growth"); err != nil || next.Level != 31 || next.SP != 6 || next.BaseMaxHP < beforeHP+48 || next.BaseMaxHP > beforeHP+52 || next.BaseMaxMP < beforeMP+4 || next.BaseMaxMP > beforeMP+6 || next.AP != c.AP+5 {
		t.Fatalf("growth %+v %v", next, err)
	}
	if _, e := content.PhysicalAttackProfileFor("", next, 1001004, time.Now()); e == nil {
		t.Fatal("missing actual weapon accepted")
	}
}

func TestQAGMSpearmanHealLevelWorksWithoutOpeningArbitraryJobSetter(t *testing.T) {
	c := gmPolicyCharacter()
	c.Job = 130
	c.Level = 30
	changed, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "heal"}, time.Now())
	if err != nil || !changed || c.HP != c.MaxHP || c.MP != c.MaxMP {
		t.Fatal("reviewed130 heal", err)
	}
	if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "level", Args: []int64{31}, OperationID: "spearman-qa-level-source"}, time.Now()); err != nil || c.Level != 31 {
		t.Fatal("reviewed130 level", err)
	}
	v := gmPolicyCharacter()
	v.Job = 100
	v.Level = 30
	// Raymond approved QA job setting for skill acceptance: the job changes,
	// stats and SP stay as they were (no advancement rewards).
	before := v
	if changed, _, _, err = applyQAGMPolicy(&v, domain.GMCommandRequest{Name: "job", Args: []int64{130}}, time.Now()); err != nil || !changed || v.Job != 130 || v.STR != before.STR || v.SP != before.SP || v.MaxHP != before.MaxHP {
		t.Fatal("QA second job", v, err)
	}
}
