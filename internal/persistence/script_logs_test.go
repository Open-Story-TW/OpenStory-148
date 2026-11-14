package persistence

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestScriptLogCalendarBoundaries(t *testing.T) {
	// Monday March 1 00:00 in UTC+08, spanning week/month/year boundaries.
	now := time.Date(2027, 2, 28, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		period domain.LogPeriod
		want   string
	}{
		{domain.LogPermanent, "1970-01-01T00:00:00Z"},
		{domain.LogHourly, "2027-02-28T16:00:00Z"},
		{domain.LogDaily, "2027-02-28T16:00:00Z"},
		{domain.LogWeekly, "2027-02-28T16:00:00Z"},
		{domain.LogMonthly, "2027-02-28T16:00:00Z"},
		{domain.LogYearly, "2026-12-31T16:00:00Z"},
	} {
		start, err := ScriptLogWindow(now, tc.period)
		if err != nil || start.Format(time.RFC3339) != tc.want {
			t.Fatalf("%s=%s error=%v", tc.period, start, err)
		}
	}
	previous := now.Add(-time.Nanosecond)
	week, err := ScriptLogWindow(previous, domain.LogWeekly)
	if err != nil || week.Format(time.RFC3339) != "2027-02-21T16:00:00Z" {
		t.Fatalf("previous week=%s error=%v", week, err)
	}
	if _, err = ScriptLogWindow(now, "rolling-month"); err == nil {
		t.Fatal("unknown period accepted")
	}
	a := domain.CharacterSession{AccountID: 5, CharacterID: 9, Lease: domain.WorldLease{WorldID: 0}}
	b := a
	b.CharacterID = 10
	key := domain.ScriptLogKey{Scope: domain.LogAccount, Period: domain.LogDaily, Name: "daily"}
	ka, _ := logKey(a, key, now)
	kb, _ := logKey(b, key, now)
	if ka.owner != kb.owner {
		t.Fatal("account log was split by character")
	}
	key.Scope = domain.LogCharacter
	ka, _ = logKey(a, key, now)
	kb, _ = logKey(b, key, now)
	if ka.owner == kb.owner {
		t.Fatal("character log was shared")
	}
}
