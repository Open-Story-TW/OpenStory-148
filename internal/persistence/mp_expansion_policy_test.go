package persistence

import (
	"maple-go-server/internal/domain"
	"reflect"
	"testing"
	"time"
)

func TestQAGMMPExpansionIsBoundedMageSetupWithoutSPMPOrBuffGrant(t *testing.T) {
	c := gmPolicyCharacter()
	c.Job = 200
	c.Skills = nil
	beforeSP, beforeMP, base := c.SP, c.MP, c.BaseMaxMP
	changed, id, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{2000006, 1}}, time.Now())
	if err != nil || !changed || id != 2000006 || c.SP != beforeSP || c.MP != beforeMP || c.MaxMP != base+base*2/100 || c.BaseMaxMP != base || len(c.SkillBuffs) != 0 || len(c.SkillCooldowns) != 0 {
		t.Fatalf("QA setup %+v %v", c, err)
	}
	before := cloneCharacterSkillsForMPTest(c)
	changed, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "skill", Args: []int64{2000006, 1}}, time.Now())
	if err != nil || changed || !reflect.DeepEqual(before, c) {
		t.Fatal("same level had another effect")
	}
	for _, test := range []struct{ job, model, level int }{{0, 1, 1}, {100, 1, 1}, {300, 1, 1}, {200, 0, 1}, {200, 1, 11}, {200, 1, 0}} {
		v := gmPolicyCharacter()
		v.Job = test.job
		v.VitalsModel = test.model
		before := v
		if _, _, _, err := applyQAGMPolicy(&v, domain.GMCommandRequest{Name: "skill", Args: []int64{2000006, int64(test.level)}}, time.Now()); err == nil || !reflect.DeepEqual(before, v) {
			t.Fatalf("invalid QA %+v mutated %+v %v", test, v, err)
		}
	}
}
func cloneCharacterSkillsForMPTest(c domain.Character) domain.Character {
	c.Skills = append([]domain.SkillState(nil), c.Skills...)
	return c
}
