package all

import (
	"os"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// Buffs feed the attacker stats like Java PlayerStats.
func TestPlayerStatsAddsBuffsAndMapleWarrior(t *testing.T) {
	now := time.Now()
	c := domain.Character{Job: 110, Level: 120, STR: 400, DEX: 100, INT: 4, LUK: 4,
		Equipment: []domain.Equipment{{Slot: -11, ItemID: 1302000, WATK: 100, STR: 20}}}
	base := skills.PlayerStats(c, now)
	if base.STR != 420 || base.WATK != 100 {
		t.Fatalf("base %+v", base)
	}
	c.SkillBuffs = []domain.SkillBuff{
		{ID: 1121000, Stat: "MAPLE_WARRIOR", Value: 10, ExpiresAt: now.Add(time.Minute)},
		{ID: 1101006, Stat: "WATK", Value: 20, ExpiresAt: now.Add(time.Minute)},
		{ID: 3121002, Stat: "SHARP_EYES", Value: 15<<8 | 140, ExpiresAt: now.Add(time.Minute)},
		{ID: 1001003, Stat: "WDEF", Value: 9, ExpiresAt: now.Add(-time.Second)},
	}
	got := skills.PlayerStats(c, now)
	// Maple Warrior scales base STR (400) only: 420 + 40.
	if got.STR != 460 || got.DEX != 110 || got.WATK != 120 || got.CritRate != base.CritRate+15 || got.CritDamage != base.CritDamage+140 {
		t.Fatalf("buffed %+v", got)
	}
}

func TestTakenDamageMagicGuard(t *testing.T) {
	now := time.Now()
	c := domain.Character{HP: 500, MP: 30}
	if hp, mp := skills.TakenDamage(c, 100, now); hp != 100 || mp != 0 {
		t.Fatalf("no guard %d/%d", hp, mp)
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 2001002, Stat: "MAGIC_GUARD", Value: 80, ExpiresAt: now.Add(time.Minute)}}
	// 80 of 100 goes to MP, but only 30 MP remain: the rest falls back to HP.
	if hp, mp := skills.TakenDamage(c, 100, now); hp != 70 || mp != 30 {
		t.Fatalf("guard %d/%d", hp, mp)
	}
}

func TestV148LearnedMovementFromEmbeddedBooks(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	now := time.Now()
	for _, tc := range []struct {
		job, skill, rank int
		speed, jump      int
	}{
		{300, 3000002, 15, 160, 123},
		{1300, 13000001, 15, 160, 123},
		{400, 4001005, 20, 150, 123},
		{2100, 21001001, 5, 160, 123},
		{2400, 24001002, 20, 160, 123},
	} {
		c := domain.Character{Job: tc.job, Level: 30,
			Skills:    []domain.SkillState{{ID: tc.skill, Level: tc.rank}},
			Equipment: []domain.Equipment{{Slot: -7, Speed: 50, Jump: 30}}}
		if got := skills.PlayerStats(c, now); got.Speed != tc.speed || got.Jump != tc.jump {
			t.Errorf("skill %d gives speed %d / jump %d, want %d / %d", tc.skill, got.Speed, got.Jump, tc.speed, tc.jump)
		}
	}
}

func TestMercedesPotentialPowerMovement(t *testing.T) {
	const potentialPower = 23000001
	now := time.Unix(1000, 0)
	for _, tc := range []struct {
		rank, equipmentSpeed, speed int
	}{
		{0, 0, 100},
		{15, 0, 130},
		{0, 60, 140},
		{15, 60, 165},
	} {
		c := domain.Character{Job: 2300, Level: 120}
		if tc.rank != 0 {
			c.Skills = []domain.SkillState{{ID: potentialPower, Level: tc.rank}}
		}
		if tc.equipmentSpeed != 0 {
			c.Equipment = []domain.Equipment{{Slot: -7, Speed: tc.equipmentSpeed}}
		}
		if got := skills.PlayerStats(c, now); got.Speed != tc.speed {
			t.Errorf("rank %d, equipment speed %d: speed %d, want %d", tc.rank, tc.equipmentSpeed, got.Speed, tc.speed)
		}
	}
}

func TestV148ThunderBreakerExtremeDodgeMovement(t *testing.T) {
	if os.Getenv("MAPLEGO_WZ_ROOT") == "" {
		t.Skip("original v148 WZ required")
	}
	const extremeDodge = 15000000
	now := time.Unix(1000, 0)
	c := domain.Character{Job: 1500, Level: 30}
	before := skills.PlayerStats(c, now)
	c.Skills = []domain.SkillState{{ID: extremeDodge, Level: 10}}
	after := skills.PlayerStats(c, now)
	if after.Accuracy-before.Accuracy != 220 || after.Jump != 110 || after.Speed != 100 || after.Avoid != before.Avoid {
		t.Fatalf("base %+v, Extreme Dodge %+v", before, after)
	}
	c.Equipment = []domain.Equipment{{Slot: -7, Speed: 60}}
	if got := skills.PlayerStats(c, now); got.Speed != 160 {
		t.Fatalf("equipped speed %d, want 160", got.Speed)
	}
}

func TestShadowPartnerUsesSourceSkillX(t *testing.T) {
	now := time.Now()
	c := domain.Character{STR: 4, DEX: 4, INT: 4, LUK: 4, SkillBuffs: []domain.SkillBuff{{ID: 4211008, Level: 20, Stat: "SHADOWPARTNER", Value: 20, ExpiresAt: now.Add(time.Minute)}}}
	if got := skills.PlayerStats(c, now).ShadowPartner; got == 20 || got <= 0 {
		t.Fatalf("shadow partner %d must be the WZ x, not the level", got)
	}
}

// Registered MaxHP% passives reach the server maxima (P 5100009) and the
// warrior's HP Increase is not counted twice.
func TestRegisteredHPPassivesFeedDerivedVitals(t *testing.T) {
	now := time.Now()
	c := domain.Character{Job: 510, Level: 70, VitalsModel: 1, BaseMaxHP: 559, BaseMaxMP: 300, Skills: []domain.SkillState{{ID: 5100009, Level: 10}}}
	hp, _, err := content.DerivedVitals(c, now)
	def, _ := content.SkillDefinition("", 5100009)
	v, _ := def.Values(10)
	if err != nil || hp != 559+559*v["mhpR"]/100 || v["mhpR"] == 0 {
		t.Fatalf("pirate HP %d (mhpR %d) %v", hp, v["mhpR"], err)
	}
	w := domain.Character{Job: 110, Level: 30, VitalsModel: 1, BaseMaxHP: 100, BaseMaxMP: 10, Skills: []domain.SkillState{{ID: 1000006, Level: 1}}}
	if hp, _, err = content.DerivedVitals(w, now); err != nil || hp != 102 {
		t.Fatalf("warrior HP Increase counted once: %d %v", hp, err)
	}
}

func TestDemonFuryAndRecipientBuffStats(t *testing.T) {
	now := time.Now()
	ds := domain.Character{Job: 3112, Level: 120, VitalsModel: 1, BaseMaxHP: 5000, BaseMaxMP: 900, Skills: []domain.SkillState{{ID: 2000006, Level: 10}}}
	if _, mp, err := content.DerivedVitals(ds, now); err != nil || mp != 120 {
		t.Fatalf("demon fury max %d %v", mp, err)
	}
	c := domain.Character{Job: 100, VitalsModel: 1, BaseMaxHP: 100, BaseMaxMP: 10, SkillBuffs: []domain.SkillBuff{
		{ID: 32121003, Stat: "CONVERSION", Value: 30, ExpiresAt: now.Add(time.Minute)},
		{ID: 1301007, Stat: "MAXHP", Value: 60, ExpiresAt: now.Add(time.Minute)},
	}}
	if hp, _, err := content.DerivedVitals(c, now); err != nil || hp != 130 {
		t.Fatalf("conversion replaces MAXHP: %d %v", hp, err)
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 32101002, Level: 5, Stat: "ABNORMAL_STATUS_R", Value: 12, ExpiresAt: now.Add(time.Minute)}, {ID: 32101002, Level: 5, Stat: "ELEMENTAL_STATUS_R", Value: 7, ExpiresAt: now.Add(time.Minute)}}
	if b, _ := skills.BuffBonus(c, now); b.ASR != 12 || b.TER != 7 {
		t.Fatalf("resistances %+v", b)
	}
}

func TestTakenDamageReductions(t *testing.T) {
	now := time.Now()
	c := domain.Character{HP: 500, MP: 30, SkillBuffs: []domain.SkillBuff{{ID: 23111005, Stat: "WATER_SHIELD", Value: 40, ExpiresAt: now.Add(time.Minute)}}}
	if hp, mp := skills.TakenDamage(c, 100, now); hp != 60 || mp != 0 {
		t.Fatalf("water shield %d/%d", hp, mp)
	}
}
