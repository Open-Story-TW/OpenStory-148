package domain

import "testing"

func TestMihileSkillBooks(t *testing.T) {
	for i, job := range []int{5100, 5110, 5111, 5112} {
		if !ValidJob(job) || !ExtendedSPJob(job) || SkillBook(job) != i {
			t.Errorf("Mihile job %d does not use SP book %d", job, i)
		}
		if !CanLearnSkill(job, 51000000) || !CanLearnSkill(job, 50001000) {
			t.Errorf("Mihile job %d cannot learn its earlier books", job)
		}
	}
	if CanLearnSkill(5100, 51100000) || CanLearnSkill(112, 51000000) || ValidJob(5113) {
		t.Fatal("Mihile skill access crossed its job progression")
	}
}
