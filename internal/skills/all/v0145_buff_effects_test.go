package all_test

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/all"
)

func TestInvincibleDamageReduction(t *testing.T) {
	now := time.Now()
	cleric := domain.Character{
		Job: 230, HP: 1000, MaxHP: 1000,
		SkillBuffs: []domain.SkillBuff{{ID: 2301003, Level: 15, Stat: "INVINCIBLE", Value: 20, ExpiresAt: now.Add(time.Minute)}},
	}
	hp, mp, _ := skills.TakenDamageWithMesos(cleric, 1000, now)
	if hp != 800 || mp != 0 {
		t.Fatalf("cleric invincible 20%%: got hp=%d mp=%d, want 800/0", hp, mp)
	}
	// Buccaneer 5111010 already supplies Bonus.DamageReduce via Passive and must not double-reduce.
	bucc := domain.Character{
		Job: 511, HP: 1000, MaxHP: 1000,
		Skills:     []domain.SkillState{{ID: 5111010, Level: 20}},
		SkillBuffs: []domain.SkillBuff{{ID: 5111010, Level: 20, Stat: "INVINCIBLE", Value: 20, ExpiresAt: now.Add(time.Minute)}},
	}
	hp2, _, _ := skills.TakenDamageWithMesos(bucc, 1000, now)
	if hp2 != 800 {
		t.Fatalf("buccaneer 5111010 must reduce once (20%%): got hp=%d, want 800", hp2)
	}
}

func TestBlessHolySymbolYellowAuraBuffBonus(t *testing.T) {
	now := time.Now()
	// 2301004 Cleric Bless lv15: x=15, y=15, z=300, u=300, v=180, w=180.
	cBless := domain.Character{
		Job: 230,
		SkillBuffs: []domain.SkillBuff{{ID: 2301004, Level: 15, Stat: "BLESS", Value: 15, ExpiresAt: now.Add(time.Minute)}},
	}
	b, _ := skills.BuffBonus(cBless, now)
	if b.WATK != 15 || b.MATK != 15 || b.WDEF != 300 || b.MDEF != 300 || b.ACC != 180 || b.AVOID != 180 {
		t.Fatalf("bless lv15 bonus: got %+v", b)
	}
	// 9001003 GM Bless carries explicit WATK/MATK stats alongside BLESS and must not double-count.
	cGMBless := domain.Character{
		Job: 900,
		SkillBuffs: []domain.SkillBuff{
			{ID: 9001003, Level: 1, Stat: "WATK", Value: 20, ExpiresAt: now.Add(time.Minute)},
			{ID: 9001003, Level: 1, Stat: "BLESS", Value: 1, ExpiresAt: now.Add(time.Minute)},
		},
	}
	bGM, _ := skills.BuffBonus(cGMBless, now)
	if bGM.WATK != 20 {
		t.Fatalf("gm bless must not double-count: got watk=%d, want 20", bGM.WATK)
	}
	// 2311003 Holy Symbol (+50% EXP) and 9001002 GM Holy Symbol (+50% EXP).
	for _, id := range []int{2311003, 9001002} {
		cHS := domain.Character{
			SkillBuffs: []domain.SkillBuff{{ID: id, Stat: "HOLY_SYMBOL", Value: 50, ExpiresAt: now.Add(time.Minute)}},
		}
		bHS, _ := skills.BuffBonus(cHS, now)
		if bHS.EXP != 50 {
			t.Fatalf("holy symbol %d: got exp=%d, want 50", id, bHS.EXP)
		}
	}
	// 32101003 Yellow Aura lv20: speed +20.
	cYA := domain.Character{
		Job: 3210,
		SkillBuffs: []domain.SkillBuff{{ID: 32101003, Level: 20, Stat: "YELLOW_AURA", Value: 20, ExpiresAt: now.Add(time.Minute)}},
	}
	bYA, _ := skills.BuffBonus(cYA, now)
	if bYA.Speed != 20 {
		t.Fatalf("yellow aura lv20 speed: got %d, want 20", bYA.Speed)
	}
}
