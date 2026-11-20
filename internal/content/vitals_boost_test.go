package content

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestDerivedVitalsAddsFlatBoostsAfterPercent(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Minute)
	c := domain.Character{Job: 211, VitalsModel: 1, BaseMaxHP: 1000, BaseMaxMP: 3396, SkillBuffs: []domain.SkillBuff{
		{ID: 1, Stat: "MAXMP", Value: 10, ExpiresAt: later},
		{ID: 2, Stat: "MP_BOOST", Value: 500, ExpiresAt: later},
		{ID: 2, Stat: "HP_BOOST", Value: 300, ExpiresAt: later},
		{ID: 3, Stat: "ENHANCED_MAXHP", Value: 50, ExpiresAt: later},
		{ID: 4, Stat: "MP_BOOST", Value: 999, ExpiresAt: now.Add(-time.Second)},
	}}
	hp, mp, err := DerivedVitals(c, now)
	if err != nil || hp != 1350 || mp != 3396+339+500 {
		t.Fatal(hp, mp, err)
	}
}

func TestV148WillMaxHPEntersBeforePercent(t *testing.T) {
	now := time.Unix(100, 0)
	c := domain.Character{VitalsModel: 1, BaseMaxHP: 4496, BaseMaxMP: 2987,
		SkillBuffs: []domain.SkillBuff{
			{Stat: "MAXHP", Value: 60, ExpiresAt: now.Add(time.Minute)},
			{Stat: "MAXMP", Value: 40, ExpiresAt: now.Add(time.Minute)},
		},
	}
	for _, sample := range []struct{ exp, hp int }{{180, 7193}, {181, 7353}, {domain.MaxTraitEXP, 10393}} {
		c.Traits.Will = sample.exp
		hp, mp, err := DerivedVitals(c, now)
		if err != nil || hp != sample.hp || mp != 4181 || c.WireMaxHP() != 4496 || c.WireMaxMP() != 2987 {
			t.Fatalf("Will EXP %d yielded HP/MP %d/%d (%v), want %d/4181 with intrinsic packet maxima", sample.exp, hp, mp, err, sample.hp)
		}
	}
}
