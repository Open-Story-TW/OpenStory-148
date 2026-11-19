package thief

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func context(t *testing.T, id, level int, caster domain.Character) *skills.CastContext {
	t.Helper()
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	values, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return &skills.CastContext{Skill: def, Level: level, Values: values, Caster: caster, Now: time.Unix(0, 0)}
}

func plan(t *testing.T, build func(*skills.CastContext) (skills.CastPlan, error), id, level int, caster domain.Character) skills.CastPlan {
	t.Helper()
	p, err := build(context(t, id, level, caster))
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func registered(t *testing.T, id int) skills.Handler {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok {
		t.Fatalf("skill %d not registered", id)
	}
	return h
}

func buff(stats ...skills.StatValue) []skills.StatValue { return stats }

func TestThiefWeaponAttackBuffsKeepLearnedDefenseIgnore(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		id, job, rank, attack int
		attackStat            buffstat.Stat
	}{
		{4121014, 412, 30, 40, "ANGEL_ATK"},
		{4221013, 422, 20, 20, "WATK"},
	} {
		character := domain.Character{Job: tc.job, Level: 200}
		if got := skills.Bonuses(character, now).IgnoreDefense; got != 0 {
			t.Fatalf("skill %d before learning: ignore defense %d", tc.id, got)
		}
		character.Skills = []domain.SkillState{{ID: tc.id, Level: tc.rank}}
		assertPassive := func(at time.Time) {
			t.Helper()
			if got := skills.StatDetail(character, at).IgnoreDefense; got != int32(tc.rank) {
				t.Errorf("skill %d at %v: ignore defense %d, want %d", tc.id, at, got, tc.rank)
			}
		}
		assertPassive(now)
		cast := plan(t, registered(t, tc.id).Cast, tc.id, tc.rank, character)
		want := buff(stat(tc.attackStat, tc.attack))
		if cast.Self == nil || !reflect.DeepEqual(cast.Self.Stats, want) {
			t.Fatalf("skill %d: cast buffs %+v, want only %v", tc.id, cast.Self, want)
		}
		expires := now.Add(cast.Self.Duration)
		character.SkillBuffs = []domain.SkillBuff{{ID: tc.id, Level: tc.rank, Stat: string(tc.attackStat), Value: tc.attack, ExpiresAt: expires}}
		assertPassive(now)
		assertPassive(expires)
	}
}

// Every skill the plan assigns to thread T is registered, and the handler
// shape matches its kind.
func TestEveryThiefSkillIsRegistered(t *testing.T) {
	raw, err := os.ReadFile("../testdata/skill-plan-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	var inv struct {
		Skills []struct {
			ID     int    `json:"id"`
			Thread string `json:"thread"`
		} `json:"skills"`
	}
	if err = json.Unmarshal(raw, &inv); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range inv.Skills {
		if s.Thread != "T" {
			continue
		}
		n++
		h := registered(t, s.ID)
		if (h.Attack != nil && h.Attack.Kind&^combat.Summoned != 0) != (h.Kind&skills.KindAttack != 0) {
			t.Errorf("skill %d: attack spec and kind disagree", s.ID)
		}
		if h.Cast == nil && h.Attack == nil && h.Kind&skills.KindPassive == 0 {
			t.Errorf("skill %d: no behaviour", s.ID)
		}
	}
	if n != 86 {
		t.Fatalf("thread T has %d skills, want 86", n)
	}
}

// Buff plans follow Java MapleStatEffect for the WZ formulas at max level.
func TestBuffPlans(t *testing.T) {
	none := domain.Character{}
	cases := []struct {
		id, level      int
		build          func(*skills.CastContext) (skills.CastPlan, error)
		mp             int
		cooldown, time time.Duration
		party          bool
		stats          []skills.StatValue
	}{
		{4001003, 1, darkSight, 23, 0, 20 * time.Second, false, buff(stat("DARK_SIGHT", 1), stat("SPEED", -27))},
		{4001003, 10, darkSight, 5, 0, 200 * time.Second, false, buff(stat("DARK_SIGHT", 1))},
		{4101003, 20, booster, 11, 0, 200 * time.Second, false, buff(stat("BOOSTER", -2))},
		{4201002, 20, booster, 10, 0, 200 * time.Second, false, buff(stat("BOOSTER", -2))},
		{4301002, 20, booster, 11, 0, 200 * time.Second, false, buff(stat("BOOSTER", -2))},
		{4101004, 20, skills.StandardPartyBuff, 30, 0, 200 * time.Second, true, buff(stat("SPEED", 40), stat("JUMP", 20))},
		{4201003, 20, skills.StandardPartyBuff, 30, 0, 200 * time.Second, true, buff(stat("SPEED", 40), stat("JUMP", 20))},
		{4311001, 20, skills.StandardBuff, 30, 0, 200 * time.Second, false, buff(stat("SPEED", 40), stat("JUMP", 23))},
		{4111001, 20, mesoUp, 60, 0, 120 * time.Second, true, buff(stat("MESOUP", 150))},
		{4111002, 20, shadowPartner, 80, 0, 180 * time.Second, false, buff(stat("SHADOWPARTNER", 50))},
		{4211008, 20, banditShadowPartner, 80, 0, 180 * time.Second, false, buff(stat("SHADOWPARTNER", 20))},
		{4121000, 30, mapleWarrior, 70, 0, 900 * time.Second, true, buff(stat("MAPLE_WARRIOR", 15))},
		{4221000, 30, mapleWarrior, 70, 0, 900 * time.Second, true, buff(stat("MAPLE_WARRIOR", 15))},
		{4341000, 30, mapleWarrior, 70, 0, 900 * time.Second, true, buff(stat("MAPLE_WARRIOR", 15))},
		{4121006, 30, shadowStars, 46, 0, 180 * time.Second, false, buff(stat("SPIRIT_CLAW", 0))},
		{4211003, 10, pickpocket, 50, 0, 150 * time.Second, false, buff(stat("PICKPOCKET", 200))},
		{4211005, 10, mesoGuard, 37, 0, 180 * time.Second, false, buff(stat("MESOGUARD", 61))},
		{4341007, 30, thorns, 90, 0, 210 * time.Second, false, buff(stat("ENHANCED_WATK", 30))},
		{4321000, 20, tornadoSpin, 20, 0, time.Second, false, buff(stat("DASH_SPEED", 255), stat("DASH_JUMP", 0))},
	}
	for _, c := range cases {
		p := plan(t, c.build, c.id, c.level, none)
		b := p.Self
		if c.party {
			b = p.Party
			if p.Self != nil {
				t.Errorf("skill %d: party buff also on self", c.id)
			}
		}
		if b == nil || p.MPCost != c.mp || p.HPCost != 0 || p.Cooldown != c.cooldown || b.Duration != c.time || !reflect.DeepEqual(b.Stats, c.stats) {
			t.Errorf("skill %d level %d: plan %+v buff %+v", c.id, c.level, p, b)
		}
	}
}

func TestMirrorImageOnlyForBladeLordAndMaster(t *testing.T) {
	p := plan(t, mirrorImage, 4331002, 30, domain.Character{Job: 434})
	if p.MPCost != 68 || p.Self == nil || p.Self.Duration != 180*time.Second || !reflect.DeepEqual(p.Self.Stats, buff(stat("SHADOWPARTNER", 70))) {
		t.Fatalf("mirror image %+v %+v", p, p.Self)
	}
	if _, err := mirrorImage(context(t, 4331002, 30, domain.Character{Job: 432})); err == nil {
		t.Fatal("mirror image allowed for job 432")
	}
}

func TestV148PickpocketUsesToggle(t *testing.T) {
	def, err := content.SkillDefinition(t.TempDir(), 4211003)
	if err != nil {
		t.Fatal(err)
	}
	values, err := def.Values(def.MaxLevel)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pickpocket(&skills.CastContext{Skill: def, Level: def.MaxLevel, Values: values})
	if err != nil || !p.Toggle || p.MPCost != 0 || p.Self == nil || p.Self.Duration != skills.Permanent || !reflect.DeepEqual(p.Self.Stats, buff(stat("PICKPOCKET", 200))) {
		t.Fatalf("v148 pickpocket plan %+v buff %+v err %v", p, p.Self, err)
	}
}

func TestShadowPartnerTakesASummoningRock(t *testing.T) {
	for _, id := range []int{4111002, 4211008} {
		p := plan(t, registered(t, id).Cast, id, 20, domain.Character{})
		if len(p.Items) != 1 || p.Items[0] != (skills.ItemCost{ItemID: 4006001, Quantity: 1}) || p.Self == nil {
			t.Errorf("shadow partner %d: %+v", id, p)
		}
	}
}

func TestHerosWillDispels(t *testing.T) {
	for _, id := range []int{4121009, 4221008, 4341008} {
		p := plan(t, registered(t, id).Cast, id, 5, domain.Character{})
		if !p.Dispel || p.MPCost != 30 || p.Cooldown != 360*time.Second || p.Self != nil {
			t.Errorf("hero's will %d: %+v", id, p)
		}
	}
}

// Chakra follows Java calcHPChange with total LUK and DEX.
func TestChakraHealsFromLUKAndDEX(t *testing.T) {
	defer func(r func(int, int) int) { rollRange = r }(rollRange)
	rollRange = func(lo, _ int) int { return lo }
	ch := domain.Character{LUK: 300, DEX: 40, Equipment: []domain.Equipment{{Slot: -11, LUK: 100, DEX: 10}, {Slot: -101, LUK: 999}}}
	p := plan(t, chakra, 4211001, 10, ch)
	// y=300: (101×400×0.033 + 50) × 400 × 0.002 = 1106; + int(400×2.3×3) = 2759
	// (floating point, same as Java).
	if p.Heal == nil || p.Heal.HP != 1106+2759 || p.MPCost != 140 || p.Self != nil {
		t.Fatalf("chakra %+v %+v", p, p.Heal)
	}
}

func TestFlashJumpCostsOnlyMP(t *testing.T) {
	for _, id := range []int{4111006, 4211009, 4321003} {
		p := plan(t, registered(t, id).Cast, id, 1, domain.Character{})
		if p.MPCost < 1 || p.Self != nil || p.Party != nil || p.Cooldown != 0 {
			t.Errorf("flash jump %d: %+v", id, p)
		}
	}
}

func TestPassiveBonuses(t *testing.T) {
	claw := []domain.Equipment{{Slot: -11, ItemID: 1472000}}
	dagger := []domain.Equipment{{Slot: -11, ItemID: 1332000}}
	cases := []struct {
		id, level int
		char      domain.Character
		want      skills.Bonus
	}{
		{4000000, 20, domain.Character{}, skills.Bonus{ACC: 160, AVOID: 160}},
		{4100000, 20, domain.Character{Job: 412, Equipment: claw}, skills.Bonus{Mastery: 50, ACC: 120}},
		{4100000, 20, domain.Character{Job: 412, Equipment: dagger}, skills.Bonus{}},
		{4200000, 20, domain.Character{Job: 422, Equipment: dagger}, skills.Bonus{Mastery: 50, ACC: 120}},
		{4200000, 20, domain.Character{Job: 434, Equipment: dagger}, skills.Bonus{}},
		{4300000, 20, domain.Character{Job: 434, Equipment: dagger}, skills.Bonus{Mastery: 50, ACC: 120}},
		{4300000, 20, domain.Character{Job: 422, Equipment: dagger}, skills.Bonus{}},
		{4100001, 30, domain.Character{}, skills.Bonus{Critical: 50, CriticalDamageMin: 15}},
		{4340010, 10, domain.Character{}, skills.Bonus{Critical: 25, CriticalDamageMin: 20}},
		{4100006, 20, domain.Character{}, skills.Bonus{MaxHPPercent: 10, ASR: 20, TER: 20}},
		{4200006, 20, domain.Character{}, skills.Bonus{MaxHPPercent: 10, ASR: 20, TER: 20}},
		{4310004, 20, domain.Character{}, skills.Bonus{MaxHPPercent: 10, ASR: 20, TER: 20}},
		{4000001, 5, domain.Character{}, skills.Bonus{Range: 200}},
		{4110000, 20, domain.Character{}, skills.Bonus{PotionPercent: 50}},
		{4120002, 30, domain.Character{}, skills.Bonus{AvoidRate: 45}},
		{4220002, 30, domain.Character{}, skills.Bonus{AvoidRate: 45}},
		{4210000, 10, domain.Character{}, skills.Bonus{WDEFPercent: 100, MDEFPercent: 100}},
		{4220009, 10, domain.Character{}, skills.Bonus{MesoRate: 20}},
		{4341006, 30, domain.Character{Job: 434}, skills.Bonus{WDEFPercent: 60, MDEFPercent: 60, AvoidRate: 25}},
		{4341006, 30, domain.Character{Job: 432}, skills.Bonus{}},
	}
	for _, c := range cases {
		h := registered(t, c.id)
		ctx := context(t, c.id, c.level, c.char)
		got := h.Passive(&skills.PassiveContext{Skill: ctx.Skill, Level: c.level, Values: ctx.Values, Character: c.char})
		if got != c.want {
			t.Errorf("skill %d job %d: %+v, want %+v", c.id, c.char.Job, got, c.want)
		}
	}
}

func TestAdvancedThiefMasteryReplacesBasicMastery(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		job, weapon, basicSkill, advancedSkill int
	}{
		{412, 1472000, 4100000, 4120012},
		{422, 1332000, 4200000, 4220012},
		{434, 1332000, 4300000, 4340013},
	} {
		c := domain.Character{Job: tc.job, Equipment: []domain.Equipment{{Slot: -11, ItemID: tc.weapon}}, Skills: []domain.SkillState{{ID: tc.basicSkill, Level: 20}}}
		before := skills.Bonuses(c, now)
		if before.Mastery != 50 || before.ACC != 120 {
			t.Errorf("job %d basic mastery/ACC = %d/%d", tc.job, before.Mastery, before.ACC)
		}
		c.Skills = append(c.Skills, domain.SkillState{ID: tc.advancedSkill, Level: 30})
		after := skills.Bonuses(c, now)
		if after.Mastery != 70 || after.ACC != before.ACC || after.WATK != 30 {
			t.Errorf("job %d advanced mastery/ACC/WATK = %d/%d/%d", tc.job, after.Mastery, after.ACC, after.WATK)
		}
		c.Skills[1].ExpiresAt = now.Add(-time.Second)
		expired := skills.Bonuses(c, now)
		if expired.Mastery != before.Mastery || expired.ACC != before.ACC || expired.WATK != before.WATK {
			t.Errorf("job %d expired mastery/ACC/WATK = %d/%d/%d", tc.job, expired.Mastery, expired.ACC, expired.WATK)
		}
	}
}

// The v148 book drops Pickpocket's time: the cast toggles a permanent buff.
func TestV148PickpocketToggles(t *testing.T) {
	c := context(t, 4211003, 10, domain.Character{})
	delete(c.Values, "time")
	p, err := pickpocket(c)
	if err != nil || !p.Toggle || p.Self == nil || p.Self.Duration != skills.Permanent || !reflect.DeepEqual(p.Self.Stats, buff(stat("PICKPOCKET", 200))) {
		t.Fatalf("v148 pickpocket %+v %+v %v", p, p.Self, err)
	}
}
