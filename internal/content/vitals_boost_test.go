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
