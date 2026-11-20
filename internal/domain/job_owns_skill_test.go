package domain

import "testing"

func TestJobOwnsSkillMatchesTheClientShadowPartnerLookup(t *testing.T) {
	for _, tc := range []struct {
		job, skill int
		want       bool
	}{
		{411, 4111002, true}, {412, 4111002, true}, {410, 4111002, false}, {400, 4111002, false},
		{434, 4331002, true}, {433, 4331002, true}, {432, 4331002, false}, {500, 4331002, false},
		{422, 4211008, true}, {1411, 14111000, true}, {412, 14111000, false},
		{400, 4001003, true}, {500, 80001000, true},
	} {
		if got := JobOwnsSkill(tc.job, tc.skill); got != tc.want {
			t.Errorf("JobOwnsSkill(%d, %d) = %v, want %v", tc.job, tc.skill, got, tc.want)
		}
	}
}
