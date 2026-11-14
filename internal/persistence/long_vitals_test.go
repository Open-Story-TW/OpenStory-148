package persistence

import (
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestQAGMVitalsSetsBaseMaximaUpToTheCeiling(t *testing.T) {
	defer domain.SetLongVitalsForTest(true)()
	c := gmPolicyCharacter()
	changed, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "vitals", Args: []int64{9_999_999, 250_000}}, time.Now())
	if err != nil || !changed || c.BaseMaxHP != 9_999_999 || c.BaseMaxMP != 250_000 || c.MaxHP != 9_999_999 || c.HP != c.MaxHP || c.MP != c.MaxMP {
		t.Fatalf("vitals %+v %v", c, err)
	}
	if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "vitals", Args: []int64{10_000_000, 1}}, time.Now()); err == nil || !strings.Contains(err.Error(), "max HP must be 1..9999999") {
		t.Fatalf("above the ceiling accepted: %v", err)
	}
	domain.SetLongVitalsForTest(false)
	c = gmPolicyCharacter()
	if _, _, _, err = applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "vitals", Args: []int64{100_000, 1}}, time.Now()); err == nil || !strings.Contains(err.Error(), "max HP must be 1..99999") {
		t.Fatalf("above the native cap accepted with long vitals off: %v", err)
	}
}

func TestLevelUpGrowsVitalsPastTheNativeCap(t *testing.T) {
	defer domain.SetLongVitalsForTest(true)()
	c := gmPolicyCharacter()
	c.Job, c.Level, c.EXP, c.BaseMaxHP, c.BaseMaxMP = 100, 30, 0, 99_990, 99_995
	if _, _, _, err := applyQAGMPolicy(&c, domain.GMCommandRequest{Name: "level", Args: []int64{32}, OperationID: "qa-long-vitals-level"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if c.BaseMaxHP <= 99_999 || c.BaseMaxMP <= 99_999 || c.MaxHP != c.BaseMaxHP {
		t.Fatalf("level-up stopped at the native cap: %+v", c)
	}
}
