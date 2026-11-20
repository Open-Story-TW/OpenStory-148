package all

import (
	"os"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestV148DemonMasterySelectsAdvancedTier(t *testing.T) {
	now := time.Unix(100, 0)
	actor := domain.Character{
		Job: 3112, Level: 200,
		Equipment: []domain.Equipment{{Slot: -11, ItemID: 1322005}},
	}
	start := skills.Bonuses(actor, now)
	if start.Mastery != 0 {
		t.Fatalf("untrained mastery = %d, want 0", start.Mastery)
	}
	actor.Skills = []domain.SkillState{{ID: 31100004, Level: 20}}
	basic := skills.Bonuses(actor, now)
	if basic.Mastery != 50 || basic.ACC-start.ACC != 120 {
		t.Fatalf("basic mastery or accuracy differs: %+v", basic)
	}
	actor.Skills = append(actor.Skills, domain.SkillState{ID: 31120008, Level: 30})
	for _, weapon := range []int{1322005, 1312000} {
		actor.Equipment[0].ItemID = weapon
		got := skills.Bonuses(actor, now)
		if got.Mastery != 70 || got.ACC != basic.ACC || got.WATK-basic.WATK != 30 ||
			got.CriticalDamageMin-basic.CriticalDamageMin != 15 {
			t.Fatalf("weapon %d: advanced mastery stacked or lost an independent benefit: %+v", weapon, got)
		}
	}
	actor.Skills[1].ExpiresAt = now.Add(-time.Second)
	if expired := skills.Bonuses(actor, now); expired != basic {
		t.Fatalf("expired advanced skill did not restore basic mastery: %+v", expired)
	}
}

func TestV148MovementAndRegionalPassiveFields(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	if root == "" {
		t.Skip("original v148 WZ required")
	}
	dodge := skills.Bonus{ACC: 220, Jump: 10, SpeedLimitBonus: 20}
	archery := skills.Bonus{ACC: 225, AVOID: 225, Speed: 30, Range: 120, SpeedLimitBonus: 20}
	for _, tc := range []struct {
		id, job, level int
		want           skills.Bonus
	}{
		{5080000, 508, 10, dodge},
		{15000000, 1500, 10, dodge},
		{3000002, 300, 15, archery},
		{13000001, 1300, 15, archery},
		{23000001, 2300, 15, skills.Bonus{AvoidRate: 20, Speed: 30, SpeedLimitBonus: 25}},
		{5710004, 571, 20, skills.Bonus{MaxHP: 600, MaxMP: 600, ASR: 20, WDEFPercent: 30, MDEFPercent: 30}},
	} {
		def, err := content.SkillDefinition(root, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		values, err := def.Values(tc.level)
		if err != nil {
			t.Fatal(err)
		}
		c := domain.Character{Job: tc.job, Level: 200}
		h, ok := skills.Lookup(tc.id)
		if !ok || h.Passive == nil || !def.CanLearnBy(c) {
			t.Fatalf("%d has no eligible passive", tc.id)
		}
		if got := h.Passive(&skills.PassiveContext{Skill: def, Level: tc.level, Values: values, Character: c}); got != tc.want {
			t.Errorf("%d: got %+v, want %+v", tc.id, got, tc.want)
		}
	}
}

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

func TestV148ZenPassivesReachMovementDefenseAndVitals(t *testing.T) {
	now := time.Unix(1000, 0)
	actor := domain.Character{
		Job: 571, Level: 100, VitalsModel: 1, BaseMaxHP: 1800, BaseMaxMP: 1600,
		Equipment: []domain.Equipment{{Slot: -7, Speed: 60, WDEF: 581, MDEF: 602}},
	}
	base := skills.PlayerStats(actor, now)
	baseDetail := skills.StatDetail(actor, now)
	actor.Skills = []domain.SkillState{{ID: 5080000, Level: 10}, {ID: 5710004, Level: 20}}
	got := skills.PlayerStats(actor, now)
	if base.Speed != 140 || got.Speed != 160 || got.Jump != 110 || got.Accuracy-base.Accuracy != 220 {
		t.Fatalf("movement and accuracy: before %+v, after %+v", base, got)
	}
	if got.WDEF != base.WDEF+base.WDEF*30/100 || got.MDEF != base.MDEF+base.MDEF*30/100 {
		t.Fatalf("defense: before %+v, after %+v", base, got)
	}
	if detail := skills.StatDetail(actor, now); detail.StatusResist-baseDetail.StatusResist != 20 {
		t.Fatalf("status resistance: before %d, after %d", baseDetail.StatusResist, detail.StatusResist)
	}
	hp, mp, err := content.DerivedVitals(actor, now)
	if err != nil || hp != 2400 || mp != 2200 {
		t.Fatalf("vitals = %d/%d, want 2400/2200: %v", hp, mp, err)
	}
	actor.Equipment[0].Speed = 0
	if got := skills.PlayerStats(actor, now); got.Speed != 100 {
		t.Fatalf("unequipped speed %d, want 100", got.Speed)
	}
}

func TestV148CriticalRageAppliesBossRateOnlyAgainstBosses(t *testing.T) {
	now := time.Unix(1000, 0)
	actor := domain.Character{Job: 511, Level: 100}
	const unrelatedSkill = 5110000
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), unrelatedSkill)
	if err != nil {
		t.Fatal(err)
	}
	values, err := def.Values(20)
	if err != nil || values["prop"] == 0 {
		t.Fatalf("unrelated skill's critical rate: %v %v", values, err)
	}
	// v148 data gives 5110000 a subProp, so its prop only counts against a
	// stunned target and moves neither the ordinary nor the boss rate.
	_, stunnedOnly := values["subProp"]
	unrelatedBonus := values["prop"]
	if stunnedOnly {
		unrelatedBonus = 0
	}
	variants := []struct {
		name        string
		skill       domain.SkillState
		normalBonus int
		bossBonus   int
	}{
		{name: "unlearned"},
		{name: "critical rage", skill: domain.SkillState{ID: 5110011, Level: 20}, normalBonus: 15, bossBonus: 35},
		{name: "unrelated skill", skill: domain.SkillState{ID: unrelatedSkill, Level: 20}, normalBonus: unrelatedBonus, bossBonus: unrelatedBonus},
		{name: "expired", skill: domain.SkillState{ID: 5110011, Level: 20, ExpiresAt: now.Add(-time.Second)}},
	}
	const attackCriticalRate = 7
	base := skills.PlayerStats(actor, now).CritRate + attackCriticalRate
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			c := actor
			if tc.skill.ID != 0 {
				c.Skills = []domain.SkillState{tc.skill}
			}
			stats := skills.PlayerStats(c, now)
			if ordinary, boss := stats.CriticalRateAgainst(attackCriticalRate, false), stats.CriticalRateAgainst(attackCriticalRate, true); ordinary != base+tc.normalBonus || boss != base+tc.bossBonus {
				t.Fatalf("ordinary/boss critical = %d/%d, want %d/%d", ordinary, boss, base+tc.normalBonus, base+tc.bossBonus)
			}
		})
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
	c = domain.Character{Job: 112, Level: 200, VitalsModel: 1, BaseMaxHP: 4422, BaseMaxMP: 2979, Skills: []domain.SkillState{{ID: 80000000, Level: 1}}}
	if hp, mp, err := content.DerivedVitals(c, now); err != nil || hp != 4643 || mp != 3127 {
		t.Fatalf("linked Pirate Blessing maxima: %d/%d %v", hp, mp, err)
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

func TestV148PassivesPopulateStatsWithUnaffixedWeapon(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	now := time.Unix(100, 0)
	walker := domain.Character{Job: 1411, Level: 100, STR: 10, DEX: 100, INT: 10, LUK: 100,
		Skills: []domain.SkillState{{ID: 14110009, Level: 20}}, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1472000}}}
	walkerStats, err := skills.PlayerStatsChecked(walker, now)
	if err != nil || walkerStats.ASR != 30 || walkerStats.TER != 30 {
		t.Fatalf("Night Walker resistance ASR=%d TER=%d err=%v", walkerStats.ASR, walkerStats.TER, err)
	}
	breaker := domain.Character{Job: 1500, Level: 30, STR: 100, DEX: 100, INT: 10, LUK: 10,
		Skills: []domain.SkillState{{ID: 15000006, Level: 10}}, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1482000}}}
	breakerStats, err := skills.PlayerStatsChecked(breaker, now)
	if err != nil || breakerStats.CritRate != 20 || breakerStats.CritDamageMin != 10 {
		t.Fatalf("Thunder Breaker critical rate=%d minimum=%d err=%v", breakerStats.CritRate, breakerStats.CritDamageMin, err)
	}
}

func TestV148RangedMasterySelectsAdvancedTier(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		job, basic, advanced, level, weapon, mastery, attack int
	}{
		{312, 3100000, 3120005, 30, 1452000, 70, 30},
		{322, 3200000, 3220004, 30, 1462000, 70, 30},
		{1311, 13100000, 13110003, 20, 1452000, 65, 25},
		{3312, 33100000, 33120000, 30, 1462000, 70, 30},
	} {
		c := domain.Character{Job: tc.job, Level: 200, Skills: []domain.SkillState{{ID: tc.basic, Level: 20}},
			Equipment: []domain.Equipment{{Slot: -11, ItemID: tc.weapon}}}
		base := skills.Bonuses(c, now)
		if base.Mastery != 50 || base.ACC != 120 {
			t.Errorf("job %d prerequisite mastery/accuracy = %d/%d", tc.job, base.Mastery, base.ACC)
		}
		c.Skills = append(c.Skills, domain.SkillState{ID: tc.advanced, Level: tc.level})
		learned := skills.Bonuses(c, now)
		if learned.Mastery != tc.mastery || learned.ACC != base.ACC || learned.WATK != tc.attack || learned.CriticalDamageMin != 15 {
			t.Errorf("job %d advanced mastery/accuracy/attack/critical = %+v", tc.job, learned)
		}
		c.Equipment[0].ItemID = 1302000
		if wrongWeapon := skills.Bonuses(c, now); wrongWeapon.Mastery != 0 || wrongWeapon.ACC != 0 {
			t.Errorf("job %d invalid weapon mastery/accuracy = %d/%d", tc.job, wrongWeapon.Mastery, wrongWeapon.ACC)
		}
		c.Equipment[0].ItemID = tc.weapon
		c.Skills[1].ExpiresAt = now.Add(-time.Second)
		if expired := skills.Bonuses(c, now); expired.Mastery != base.Mastery || expired.ACC != base.ACC || expired.WATK != base.WATK {
			t.Errorf("job %d expired expert bonuses = %+v", tc.job, expired)
		}
	}
}
