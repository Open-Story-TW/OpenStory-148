package combat

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestAranV148SnowChargeElement(t *testing.T) {
	now := time.Unix(100, 0)
	c := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 21101006, Stat: "WK_CHARGE", ExpiresAt: now.Add(time.Minute)}}}
	if e, ok := ChargeElement(c, now); !ok || e != Ice {
		t.Fatalf("21101006 ChargeElement = (%v, %v), want (Ice, true)", e, ok)
	}
}
