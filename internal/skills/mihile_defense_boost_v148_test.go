package skills

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestV148DefenseBoostRateUsesBothDefenseBasesAndExpires(t *testing.T) {
	now := time.Unix(100, 0)
	c := domain.Character{STR: 100, DEX: 100, INT: 100, LUK: 100}
	before := PlayerStats(c, now)
	if before.WDEF != 230 || before.MDEF != 230 {
		t.Fatalf("unmodified native primary-stat defense base: %+v", before)
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 51111004, Stat: "DEFENCE_BOOST_R", Value: 100, ExpiresAt: now.Add(time.Second)}}
	after := PlayerStats(c, now)
	if after.WDEF != 460 || after.MDEF != 460 {
		t.Fatalf("Soul Endure must double both unmodified bases: %+v", after)
	}
	expired := PlayerStats(c, now.Add(time.Second))
	if expired.WDEF != before.WDEF || expired.MDEF != before.MDEF {
		t.Fatalf("expired rate still affects defense: %+v", expired)
	}
}
