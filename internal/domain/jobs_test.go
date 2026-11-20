package domain

import "testing"

func TestV148PirateJobBooks(t *testing.T) {
	for _, job := range []int{508, 509, 570, 571, 572, 580, 581, 582, 590, 591, 592} {
		if !ValidJob(job) || ExtendedSPJob(job) || SkillBook(job) != 0 || SubJobFor(job) != SubJobNone {
			t.Errorf("invalid v148 pirate book or SP layout for %d", job)
		}
	}
	for _, tc := range []struct {
		job, skill int
		allowed    bool
	}{
		{572, 5721000, true}, {582, 5821000, true}, {592, 5921000, true},
		{582, 5821010, true}, {512, 5121010, true},
		{571, 5721000, false}, {572, 5821000, false}, {592, 5821010, false},
		{512, 5821010, false}, {582, 5121010, false}, {572, 5001003, false},
		{572, 5081002, true}, {582, 5091003, true}, {592, 5091003, true},
		{582, 5081002, false}, {572, 5091003, false}, {512, 5091003, false},
		{508, 5081002, true}, {572, 5081002, true}, {572, 5700004, true}, {572, 5721001, true},
		{570, 5721001, false}, {572, 5091003, false}, {572, 5001003, false}, {522, 5081002, false},
		{582, 5091003, true}, {592, 5091003, true}, {591, 5901001, true}, {592, 5910000, true},
		{582, 5910000, false}, {592, 5801006, false}, {590, 5910000, false}, {509, 5801006, false},
		{532, 5091003, false}, {592, 5010002, false},
	} {
		if got := CanLearnSkill(tc.job, tc.skill); got != tc.allowed {
			t.Errorf("job %d, skill %d: learned=%v want %v", tc.job, tc.skill, got, tc.allowed)
		}
	}
}

func TestCanLearnSkillFollowsLegacyAncestry(t *testing.T) {
	for _, tc := range []struct {
		job, skill int
		ok         bool
	}{
		{0, 1001, true}, {100, 1001, true}, {501, 1001, true}, {1000, 1001, false}, {3000, 1001, false},
		{1000, 10001001, true}, {1100, 10001001, true}, {0, 10001001, false},
		{2000, 20001001, true}, {2100, 20001001, true}, {2001, 20001001, false}, {2002, 20001001, false},
		{2001, 20011001, true}, {2218, 20011001, true}, {2000, 20011001, false},
		{3000, 30001001, true}, {3510, 30001001, true}, {3001, 30001001, true}, {2001, 30001001, false},
		{3001, 30011001, true}, {3100, 30011001, true}, {3200, 30011001, false},
		{100, 1001004, true}, {110, 1001004, true}, {111, 1101006, true}, {120, 1101006, false},
		{100, 1101006, false}, {110, 1111002, false}, {112, 1111002, true}, {200, 1001004, false},
		{2200, 22001001, true}, {2210, 22101000, true}, {2211, 22101000, true}, {2210, 22111000, false}, {2218, 22181000, true},
		{3100, 31001000, true}, {3110, 31101000, true}, {3200, 31001000, false},
		{501, 5011000, true}, {530, 5301000, true}, {520, 5301000, false},
		{1100, 11001000, true}, {1110, 11101000, true}, {1200, 11101000, false},
	} {
		if got := CanLearnSkill(tc.job, tc.skill); got != tc.ok {
			t.Errorf("CanLearnSkill(%d,%d)=%v want %v", tc.job, tc.skill, got, tc.ok)
		}
	}
}

func TestExtendedSPMatchesClientPredicate(t *testing.T) {
	for job, want := range map[int]bool{0: false, 100: false, 1000: false, 2000: false, 2100: false, 2001: true, 2200: true, 2218: true, 2002: true, 2312: true, 2003: true, 2412: true, 5000: true, 5112: true, 3000: true, 3001: true, 3512: true} {
		if ExtendedSPJob(job) != want {
			t.Errorf("ExtendedSPJob(%d)=%v", job, !want)
		}
	}
}

func TestSetJobParksExtendedSPInOldBook(t *testing.T) {
	c := Character{Job: 2210, SP: 4}
	c.SetJob(2211)
	if c.Job != 2211 || c.SP != 0 || c.SPBooks[1] != 4 || c.SPTable()[1] != 4 || c.SPTable()[2] != 0 {
		t.Fatalf("evan advance %+v", c)
	}
	c.SP = 3
	if table := c.SPTable(); table[1] != 4 || table[2] != 3 {
		t.Fatalf("table %+v", table)
	}
	explorer := Character{Job: 100, SP: 5}
	explorer.SetJob(110)
	if explorer.SP != 5 || explorer.SPBooks != (SPBookTable{}) {
		t.Fatalf("explorer pool moved %+v", explorer)
	}
	resistance := Character{Job: 3000, SP: 2}
	resistance.SetJob(3200)
	if resistance.SP != 2 || resistance.SPBooks != (SPBookTable{}) {
		t.Fatalf("first job shares book 0 %+v", resistance)
	}
}

func TestCreationRacesMatchClientButtons(t *testing.T) {
	for _, tc := range []struct{ race, sub, job, mapID, family int }{
		{RaceResistance, 0, 3000, 931000000, 3000}, {RaceExplorer, 0, 0, 10000, 0}, {RaceExplorer, SubJobCannoneer, 0, 3000600, 0},
		{RaceCygnus, 0, 1000, 130030000, 1000}, {RaceAran, 0, 2000, 914000000, 2000}, {RaceEvan, 0, 2001, 900090004, 2001},
		{RacePhantom, 0, 2003, 915000000, 2003},
	} {
		r, ok := CreationRaceFor(tc.race, tc.sub)
		if !ok || r.Job != tc.job || r.MapID != tc.mapID || r.Family != tc.family {
			t.Errorf("race %d/%d => %+v %v", tc.race, tc.sub, r, ok)
		}
		if back, ok := CreationFamily(r.Family, r.SubJob); !ok || back != r {
			t.Errorf("family %d/%d => %+v", r.Family, r.SubJob, back)
		}
	}
	for _, race := range []int{RaceMercedes, RaceDemon, 8} {
		if _, ok := CreationRaceFor(race, 0); ok {
			t.Errorf("race %d creatable", race)
		}
	}
}

func TestSharedSkillsAreLearnableByEveryJob(t *testing.T) {
	for _, job := range []int{0, 112, 434, 532, 1512, 2112, 2218, 2312, 2412, 3112, 3512} {
		for _, skill := range []int{80001000, 80001034, 80000000, 90001001, 92000000, 92040000} {
			if !CanLearnSkill(job, skill) {
				t.Errorf("job %d cannot learn shared skill %d", job, skill)
			}
		}
		if CanLearnSkill(job, 91000000) {
			t.Errorf("job %d learned guild skill 91000000", job)
		}
	}
	if CanLearnSkill(600, 80001000) || CanLearnSkill(900, 80001000) {
		t.Error("nonexistent/GM job learned a shared skill")
	}
}

func TestBeginnerBookMatchesTheDojoSkillBook(t *testing.T) {
	for job, book := range map[int]int{
		0: 0, 112: 0, 1: 0, 532: 0, 1000: 1000, 1512: 1000, 2000: 2000, 2112: 2000,
		2001: 2001, 2218: 2001, 2002: 2002, 2312: 2002, 2003: 2003, 2412: 2003,
		3000: 3000, 3512: 3000, 3001: 3001, 3112: 3001, 5000: 5000, 5112: 5000,
	} {
		if got := BeginnerBook(job); got != book {
			t.Errorf("job %d book %d, want %d", job, got, book)
		}
	}
}
