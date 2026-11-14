package main

import "testing"

func TestPlanUsesServerJobAncestryMaximaAndPrerequisiteOrder(t *testing.T) {
	p, err := buildPlan("", 132, 200, []int{1301007, 1000006, 1301007})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skills) != 3 || p.Skills[0] != (plannedSkill{1301006, 20}) || p.Skills[1] != (plannedSkill{1301007, 20}) || p.Skills[2] != (plannedSkill{1000006, 10}) || p.OrdinaryProgressionVerified {
		t.Fatalf("bad maximum skill plan: %+v", p)
	}
	for _, job := range []int{200, 212, 2218} {
		if _, err = buildPlan("", job, 200, []int{1301007}); err == nil {
			t.Fatalf("wrong-family skill accepted for %d", job)
		}
	}
	for _, id := range []int{0, 99999999, 100000000} {
		if _, err = buildPlan("", 132, 200, []int{id}); err == nil {
			t.Fatalf("unsupported skill %d accepted", id)
		}
	}
	for _, id := range []int{-1, 123456} {
		if _, err = buildPlan("", id, 200, []int{1000006}); err == nil {
			t.Fatalf("unknown job %d accepted", id)
		}
	}
}
