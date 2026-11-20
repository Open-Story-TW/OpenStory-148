package combat

import (
	"math/rand"
	"testing"
)

func TestCriticalAuditNeverClipsAnHonestAttacker(t *testing.T) {
	for _, rate := range []int{0, 10, 40, 90} {
		for seed := int64(1); seed <= 200; seed++ {
			var a CriticalAudit
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 2000; i++ {
				// Critical rolls are capped by the client's cr, and some criticals stay under the bound.
				if r.Intn(100) < criticalAuditBase+rate {
					if !a.Admit(1, rate) {
						t.Fatalf("rate %d seed %d line %d: honest critical refused", rate, seed, i)
					}
				} else {
					a.Normal(1, rate)
				}
			}
		}
	}
}

func TestCriticalAuditStopsForgedCriticals(t *testing.T) {
	var a CriticalAudit
	admitted := 0
	for i := 0; i < 1000; i++ {
		if a.Admit(1, 0) {
			admitted++
		}
	}
	// A forger sending critical values on every line keeps roughly the 5% the rate gives.
	if admitted > 1000*15/100 {
		t.Fatalf("forged critical lines admitted: %d of 1000", admitted)
	}
}

func TestCriticalAuditIsPerCharacterAndCertainAtFullRate(t *testing.T) {
	var a CriticalAudit
	for i := 0; i < 500; i++ {
		a.Admit(1, 0)
	}
	if !a.Admit(2, 0) {
		t.Fatal("another character inherited the forger's window")
	}
	for i := 0; i < 500; i++ {
		if !a.Admit(3, 95) {
			t.Fatal("a certain critical was refused")
		}
	}
}

// Lines hit under a high rate (riding, Sharp Eyes) must not count against a
// later stretch hit at a low one: each line carries its own chance.
func TestCriticalAuditJudgesEachLineAtItsOwnRate(t *testing.T) {
	for seed := int64(1); seed <= 200; seed++ {
		var a CriticalAudit
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 600; i++ {
			rate := 40
			if i >= 200 {
				rate = 0
			}
			if r.Intn(100) < criticalAuditBase+rate {
				if !a.Admit(1, rate) {
					t.Fatalf("seed %d line %d at rate %d: honest critical refused", seed, i, rate)
				}
			} else {
				a.Normal(1, rate)
			}
		}
	}
}
