package skills

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestDojoSkillsHaveTheirImplicitLevelInTheOwnBook(t *testing.T) {
	now := time.Unix(100, 0)
	for _, c := range []struct {
		job, skill int
		ok         bool
	}{
		{112, 1009, true}, {0, 1010, true}, {532, 1011, true}, {1512, 10001010, true}, {2112, 20001011, true},
		{2218, 20011009, true}, {2312, 20021010, true}, {2412, 20031011, true}, {3512, 30001010, true},
		{3112, 30011009, true}, {5112, 50001011, true},
		{1512, 1010, false}, {112, 10001010, false}, {2218, 20001010, false}, {3112, 30001010, false},
		{112, 1012, false}, {112, 1020, false},
	} {
		character := domain.Character{Job: c.job}
		level, ok := CastLevel(character, c.skill, c.skill, 1, now)
		if ok != c.ok || ok && level != 1 {
			t.Errorf("job %d skill %d: level %d ok %v", c.job, c.skill, level, ok)
		}
		if c.ok {
			if _, ok = CastLevel(character, c.skill, c.skill, 2, now); ok {
				t.Errorf("job %d skill %d accepted level 2", c.job, c.skill)
			}
			if got := LinkedLevel(character, c.skill, c.skill, now); got != 1 {
				t.Errorf("job %d skill %d linked level %d", c.job, c.skill, got)
			}
		}
	}
	if DojoSkill(11009) || DojoSkill(1001009) || !DojoSkill(50001009) {
		t.Fatal("dojo skill set")
	}
}

func TestPowerExplosionRaisesTheDamageBound(t *testing.T) {
	now := time.Unix(100, 0)
	active := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 1011, Level: 1, Stat: "BERSERK_FURY", Value: 1, ExpiresAt: now.Add(time.Second)}}}
	if b, _ := BuffBonus(active, now); b.DamagePercent != 100 {
		t.Fatalf("damage percent %d", b.DamagePercent)
	}
	if b, _ := BuffBonus(active, now.Add(time.Second)); b.DamagePercent != 0 {
		t.Fatalf("expired: %d", b.DamagePercent)
	}
}
