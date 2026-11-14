package skills

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestInfinityTimerUsesWZAndDoesNotCatchUpOrHealAtExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, id := range []int{2121004, 2221004, 2321004} {
		b := domain.SkillBuff{ID: id, Level: 30, Stat: "INFINITY", Value: 1, ExpiresAt: now.Add(40 * time.Second), NextTick: now.Add(4 * time.Second)}
		early, h, m, e := AdvanceInfinity("", b, now.Add(4*time.Second-time.Nanosecond), 105, 59)
		if e != nil || early != b || h != 0 || m != 0 {
			t.Fatalf("early %d: %+v %d/%d %v", id, early, h, m, e)
		}
		first, h, m, e := AdvanceInfinity("", b, now.Add(4*time.Second), 105, 59)
		if e != nil || first.Value != 11 || h != 10 || m != 5 || first.ExpiresAt != b.ExpiresAt || first.NextTick != now.Add(8*time.Second) {
			t.Fatalf("first %d: %+v %d/%d %v", id, first, h, m, e)
		}
		again, h, m, e := AdvanceInfinity("", first, now.Add(4*time.Second), 105, 59)
		if e != nil || again != first || h != 0 || m != 0 {
			t.Fatalf("duplicate %d", id)
		}
		late, h, m, e := AdvanceInfinity("", first, now.Add(25*time.Second), 105, 59)
		if e != nil || late.Value != 21 || h != 10 || m != 5 || late.NextTick != now.Add(29*time.Second) {
			t.Fatalf("catch-up %d: %+v", id, late)
		}
		expired, h, m, e := AdvanceInfinity("", late, b.ExpiresAt, 105, 59)
		if e != nil || expired != late || h != 0 || m != 0 {
			t.Fatalf("expiry %d", id)
		}
		legacy := b
		legacy.NextTick = time.Time{}
		armed, h, m, e := AdvanceInfinity("", legacy, now, 105, 59)
		if e != nil || armed.Value != 1 || armed.NextTick != now.Add(4*time.Second) || h != 0 || m != 0 {
			t.Fatalf("legacy arm %d", id)
		}
		last := b
		for n := 1; n < 10; n++ {
			last, _, _, e = AdvanceInfinity("", last, now.Add(time.Duration(n)*4*time.Second), 105, 59)
			if e != nil || last.Value != 1+n*10 {
				t.Fatalf("full duration %d tick %d: %+v %v", id, n, last, e)
			}
		}
		end, h, m, e := AdvanceInfinity("", last, b.ExpiresAt, 105, 59)
		if e != nil || end.Value != 91 || h != 0 || m != 0 {
			t.Fatalf("deadline gained extra tick: %+v", end)
		}
	}
}

func TestInfinityMagicAndMapleWarriorDoNotCompoundBaseStats(t *testing.T) {
	now := time.Now()
	c := domain.Character{Level: 150, Job: 212, INT: 100, STR: 20, DEX: 20, LUK: 20, SkillBuffs: []domain.SkillBuff{
		{ID: 2121004, Stat: "INFINITY", Value: 21, ExpiresAt: now.Add(time.Minute)},
		{ID: 2121000, Stat: "MAPLE_WARRIOR", Value: 15, ExpiresAt: now.Add(time.Minute)},
	}}
	base := legacyPlayerStats(domain.Character{Level: 150, Job: 212, INT: 100, STR: 20, DEX: 20, LUK: 20}, now)
	p := legacyPlayerStats(c, now)
	if p.INT != 135 || p.STR != 23 || p.MATK != base.MATK+base.MATK*20/100 {
		t.Fatalf("stats base=%+v buffed=%+v", base, p)
	}
	c.SkillBuffs[0].ExpiresAt = now
	p = legacyPlayerStats(c, now)
	if p.INT != 115 || p.MATK != base.MATK {
		t.Fatalf("expired %+v", p)
	}
}

func TestInfinityCadenceDoesNotAccumulateSubSecondPollingJitter(t *testing.T) {
	start := time.Unix(1000, 0)
	b := domain.SkillBuff{ID: 2121004, Level: 30, Stat: "INFINITY", Value: 1, ExpiresAt: start.Add(40 * time.Second), NextTick: start.Add(4 * time.Second)}
	for i := 1; i <= 9; i++ {
		now := start.Add(time.Duration(i)*4*time.Second + time.Duration(180+i*17)*time.Millisecond)
		var err error
		b, _, _, err = AdvanceInfinity("", b, now, 1492, 5815)
		if err != nil || b.Value != 1+10*i || b.NextTick != start.Add(time.Duration(i+1)*4*time.Second) {
			t.Fatalf("tick%d at%s =>%+v %v", i, now, b, err)
		}
		early, hp, mp, err := AdvanceInfinity("", b, b.NextTick.Add(-time.Nanosecond), 1492, 5815)
		if err != nil || early != b || hp != 0 || mp != 0 {
			t.Fatalf("early repeated callback %+v %v", early, err)
		}
	}
	if b.Value != 91 || !start.Add(37*time.Second).Before(b.ExpiresAt) {
		t.Fatal("lost ninth tick before40s")
	}
	// Exactly a whole missed interval drops the backlog, retaining one heal.
	b.NextTick = start.Add(4 * time.Second)
	b, _, _, err := AdvanceInfinity("", b, start.Add(8*time.Second), 1492, 5815)
	if err != nil || b.NextTick != start.Add(12*time.Second) {
		t.Fatalf("missed interval %+v %v", b, err)
	}
}
