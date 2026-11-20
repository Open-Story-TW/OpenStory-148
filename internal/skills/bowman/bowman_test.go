package bowman_test

import (
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	_ "maple-go-server/internal/skills/bowman"
)

// pending are the thread B skills that wait for a shared freeze point.
var pending = map[int]string{}

func archer(weaponID int) domain.Character {
	c := domain.Character{Job: 312, Level: 200}
	if weaponID != 0 {
		c.Equipment = []domain.Equipment{{ItemID: weaponID, Slot: -11}}
	}
	return c
}

func values(t *testing.T, id, level int) (content.Skill, map[string]int) {
	t.Helper()
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return def, v
}

func cast(t *testing.T, id, level int, c domain.Character) (skills.CastPlan, error) {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, v := values(t, id, level)
	return h.Cast(&skills.CastContext{Skill: def, Level: level, Values: v, Caster: c, Now: time.Unix(0, 0)})
}

func mustCast(t *testing.T, id, level int, c domain.Character) skills.CastPlan {
	t.Helper()
	p, err := cast(t, id, level, c)
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func passive(t *testing.T, id, level int, c domain.Character) skills.Bonus {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Passive == nil {
		t.Fatalf("skill %d has no passive", id)
	}
	def, v := values(t, id, level)
	return h.Passive(&skills.PassiveContext{Skill: def, Level: level, Values: v, Character: c})
}

// Every enabled 300–322 skill is either registered or explicitly pending.
func TestEveryBowmanSkillIsRegisteredOrPending(t *testing.T) {
	jobs := []int{300, 310, 311, 312, 320, 321, 322}
	seen := 0
	for _, job := range jobs {
		for id := job * 10000; id < job*10000+10000; id++ {
			def, err := content.SkillDefinition("", id)
			if err != nil || def.Disabled {
				continue
			}
			seen++
			_, registered := skills.Lookup(id)
			if _, wait := pending[id]; registered == wait {
				t.Errorf("skill %d: registered=%v pending=%v", id, registered, wait)
			}
		}
	}
	if seen != 61 {
		t.Fatalf("thread B has %d enabled skills, want 61", seen)
	}
}

func TestSelfBuffsAtMaxLevel(t *testing.T) {
	c := archer(1452000)
	for _, tc := range []struct {
		id, level, mp int
		dur           time.Duration
		stats         []skills.StatValue
	}{
		{3101002, 20, 11, 200 * time.Second, []skills.StatValue{{Stat: "BOOSTER", Value: -2}}},
		{3101004, 10, 20, 300 * time.Second, []skills.StatValue{{Stat: "SOULARROW", Value: 1}}},
		{3111000, 20, 50, 180 * time.Second, []skills.StatValue{{Stat: "ENHANCED_WATK", Value: 20}, {Stat: "CONCENTRATE", Value: 25}}},
		{3121007, 30, 35, 240 * time.Second, []skills.StatValue{{Stat: "DEX", Value: 40}, {Stat: "HAMSTRING", Value: 30}}},
		{3201002, 20, 11, 200 * time.Second, []skills.StatValue{{Stat: "BOOSTER", Value: -2}}},
		{3211000, 20, 50, 180 * time.Second, []skills.StatValue{{Stat: "ENHANCED_WATK", Value: 20}}},
		{3221006, 30, 35, 240 * time.Second, []skills.StatValue{{Stat: "DEX", Value: 40}, {Stat: "BLIND", Value: 30}}},
	} {
		p := mustCast(t, tc.id, tc.level, c)
		if p.MPCost != tc.mp || p.Party != nil || p.Self == nil || p.Self.Duration != tc.dur || !reflect.DeepEqual(p.Self.Stats, tc.stats) {
			t.Errorf("skill %d: %+v self=%+v", tc.id, p, p.Self)
		}
	}
}

// Soul Arrow follows the WZ weapon field.
func TestSoulArrowNeedsItsWeapon(t *testing.T) {
	if _, err := cast(t, 3101004, 1, archer(1462000)); err == nil {
		t.Fatal("bow Soul Arrow cast with a crossbow")
	}
	if _, err := cast(t, 3201004, 1, archer(0)); err == nil {
		t.Fatal("crossbow Soul Arrow cast barehanded")
	}
	mustCast(t, 3201004, 1, archer(1462000))
}

func TestPartyBuffsUseTheWZArea(t *testing.T) {
	area := skills.Rect{Left: -400, Top: -300, Right: 400, Bottom: 300}
	for _, tc := range []struct {
		id    int
		stat  skills.StatValue
		mp    int
		dur   time.Duration
		level int
	}{
		{3121000, skills.StatValue{Stat: "MAPLE_WARRIOR", Value: 15}, 60, 900 * time.Second, 30},
		{3221000, skills.StatValue{Stat: "MAPLE_WARRIOR", Value: 15}, 70, 900 * time.Second, 30},
		{3121002, skills.StatValue{Stat: "SHARP_EYES", Value: 20<<8 | 30}, 45, 300 * time.Second, 30},
		{3221002, skills.StatValue{Stat: "SHARP_EYES", Value: 6<<8 | 1}, 30, 10 * time.Second, 1},
	} {
		p := mustCast(t, tc.id, tc.level, archer(0))
		if p.Self != nil || p.Party == nil || p.Area == nil || *p.Area != area || p.MPCost != tc.mp || p.Party.Duration != tc.dur || len(p.Party.Stats) != 1 || p.Party.Stats[0] != tc.stat {
			t.Errorf("skill %d: %+v party=%+v", tc.id, p, p.Party)
		}
	}
}

func TestDoubleJumpOnlyCostsMP(t *testing.T) {
	p := mustCast(t, 3101003, 10, archer(0))
	if p.MPCost != 10 || p.Self != nil || p.Party != nil || p.Cooldown != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestPassives(t *testing.T) {
	if b := passive(t, 3000001, 20, archer(0)); b != (skills.Bonus{Critical: 40}) {
		t.Errorf("critical shot %+v", b)
	}
	if b := passive(t, 3000002, 15, archer(0)); b != (skills.Bonus{ACC: 225, AVOID: 225, Speed: 30, Range: 120, SpeedLimitBonus: 20}) {
		t.Errorf("archery mastery %+v", b)
	}
	if b := passive(t, 3100000, 20, archer(1452000)); b != (skills.Bonus{Mastery: 50, ACC: 120}) {
		t.Errorf("bow mastery %+v", b)
	}
	if b := passive(t, 3100000, 20, archer(1462000)); b != (skills.Bonus{}) {
		t.Errorf("bow mastery with a crossbow %+v", b)
	}
	if b := passive(t, 3200000, 20, archer(1462000)); b != (skills.Bonus{Mastery: 50, ACC: 120}) {
		t.Errorf("crossbow mastery %+v", b)
	}
	if b := passive(t, 3120005, 30, archer(1452000)); b != (skills.Bonus{Mastery: 70, WATK: 30, CriticalDamageMin: 15}) {
		t.Errorf("bow expert %+v", b)
	}
	if b := passive(t, 3220004, 30, archer(1462000)); b != (skills.Bonus{Mastery: 70, WATK: 30, CriticalDamageMin: 15}) {
		t.Errorf("crossbow expert %+v", b)
	}
	for _, id := range []int{3100006, 3200006} {
		if b := passive(t, id, 10, archer(0)); b != (skills.Bonus{STR: 30, DEX: 30}) {
			t.Errorf("physical training %d %+v", id, b)
		}
	}
	for _, id := range []int{3110007, 3210007} {
		if b := passive(t, id, 10, archer(0)); b != (skills.Bonus{AvoidRate: 30}) {
			t.Errorf("evasion boost %d %+v", id, b)
		}
	}
	for _, id := range []int{3120011, 3220009} {
		if b := passive(t, id, 10, archer(0)); b != (skills.Bonus{IgnoreDefense: 25}) {
			t.Errorf("marksmanship %d %+v", id, b)
		}
	}
}

func TestHerosWillDispels(t *testing.T) {
	for _, id := range []int{3121009, 3221008} {
		p := mustCast(t, id, 5, archer(0))
		if !p.Dispel || p.MPCost != 30 || p.Cooldown != 360*time.Second || p.Self != nil || p.Party != nil {
			t.Errorf("hero's will %d %+v", id, p)
		}
	}
}

// Phoenix and Frostprey carry Spirit Link's statups once it is learned.
func TestSpiritLinkRidesTheSummonBuff(t *testing.T) {
	for _, tc := range []struct{ summon, link int }{{3111005, 3120006}, {3211005, 3220005}} {
		c := archer(0)
		if p := mustCast(t, tc.summon, 20, c); len(p.Self.Stats) != 1 {
			t.Errorf("summon %d without link %+v", tc.summon, p.Self.Stats)
		}
		c.Skills = []domain.SkillState{{ID: tc.link, Level: 30}}
		want := []skills.StatValue{{Stat: "SUMMON", Value: 1}, {Stat: "SPIRIT_LINK", Value: 1}, {Stat: "ELEMENTAL_STATUS_R", Value: 15}, {Stat: "DAMAGE_BUFF", Value: 15}}
		if p := mustCast(t, tc.summon, 20, c); !reflect.DeepEqual(p.Self.Stats, want) {
			t.Errorf("summon %d with link %+v", tc.summon, p.Self.Stats)
		}
	}
}

func TestAttacksAreShootOnly(t *testing.T) {
	for _, id := range []int{3001004, 3001005, 3100001, 3101005, 3111003, 3111004, 3111006, 3111008, 3120008, 3121004,
		3200001, 3201005, 3211003, 3211004, 3211006, 3211008, 3221001, 3221007} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || h.Cast != nil || h.Kind&skills.KindAttack == 0 {
			t.Errorf("skill %d: %+v", id, h)
		}
	}
}

func attackSpec(t *testing.T, id int) *combat.AttackSpec {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Attack == nil {
		t.Fatalf("skill %d has no attack spec", id)
	}
	return h.Attack
}

// prepared runs the spec's Prepare on the WZ effect of the given level.
func prepared(t *testing.T, id, level int, c domain.Character, now time.Time) combat.AttackContext {
	t.Helper()
	def, _ := values(t, id, level)
	e, err := combat.FromSkill(def, level)
	if err != nil {
		t.Fatal(err)
	}
	ctx := combat.AttackContext{Kind: combat.Shoot, Character: c, Effect: e, Now: now}
	if p := attackSpec(t, id).Prepare; p != nil {
		if err := p(&ctx); err != nil {
			t.Fatal(err)
		}
	}
	return ctx
}

func TestSoulArrowSkipsArrows(t *testing.T) {
	now := time.Unix(1000, 0)
	c := archer(1452000)
	if prepared(t, 3111006, 20, c, now).NoAmmo {
		t.Fatal("no Soul Arrow but no ammo")
	}
	c.SkillBuffs = []domain.SkillBuff{{ID: 3101004, Stat: "SOULARROW", Value: 1, ExpiresAt: now.Add(time.Minute)}}
	if !prepared(t, 3111006, 20, c, now).NoAmmo {
		t.Fatal("Soul Arrow still consumes arrows")
	}
	if prepared(t, 3111006, 20, c, now.Add(2*time.Minute)).NoAmmo {
		t.Fatal("expired Soul Arrow kept")
	}
	if attackSpec(t, 3111004).Ammo != combat.AmmoNone || attackSpec(t, 3211004).Ammo != combat.AmmoNone {
		t.Fatal("Arrow Rain / Arrow Eruption shoot arrows")
	}
}

func TestAttackRules(t *testing.T) {
	now := time.Unix(0, 0)
	c := archer(1452000)
	if ctx := prepared(t, 3101005, 20, c, now); ctx.Effect.Damage != 360 {
		t.Errorf("arrow bomb damage %d, want x=360", ctx.Effect.Damage)
	}
	ctx := prepared(t, 3111003, 20, c, now)
	if ctx.Effect.Element != combat.Fire || len(ctx.Effect.MonsterStatus) != 1 || !ctx.Effect.MonsterStatus[0].Poison || ctx.Effect.MonsterStatus[0].Duration != 6*time.Second {
		t.Errorf("inferno %+v", ctx.Effect)
	}
	ctx = prepared(t, 3211003, 20, c, now)
	if ctx.Effect.Element != combat.Ice || len(ctx.Effect.MonsterStatus) != 1 || ctx.Effect.MonsterStatus[0].Stat != combat.MobFreeze || ctx.Effect.MonsterStatus[0].Duration != 6*time.Second {
		t.Errorf("blizzard %+v", ctx.Effect)
	}
	if ctx := prepared(t, 3221001, 30, c, now); ctx.Effect.Lines(combat.Shoot) != 4 {
		t.Errorf("piercing lines %d", ctx.Effect.Lines(combat.Shoot))
	}
	plain := prepared(t, 3211006, 20, c, now)
	c.Skills = []domain.SkillState{{ID: 3220010, Level: 10}}
	ult := prepared(t, 3211006, 20, c, now)
	if plain.Effect.Damage != 200 || plain.Effect.Lines(combat.Shoot) != 4 || ult.Effect.Damage != 200+270-150 || ult.Effect.Lines(combat.Shoot) != 6 {
		t.Errorf("strafe %d/%d, ultimate %d/%d", plain.Effect.Damage, plain.Effect.Lines(combat.Shoot), ult.Effect.Damage, ult.Effect.Lines(combat.Shoot))
	}
	if hp, _ := attackSpec(t, 3111008).Drain(&combat.AttackContext{Character: domain.Character{MaxHP: 1000}, Effect: combat.Effect{X: 50}}, 10000); hp != 500 {
		t.Errorf("drain arrow heals %d, want maxHP/2", hp)
	}
}

func TestDamageCeilings(t *testing.T) {
	stats := combat.PlayerStats{Job: 322, Level: 200, DEX: 900, STR: 100, WATK: 200, WeaponID: 1462000}
	mob := content.Mob{HP: 5000000}
	dc := combat.DamageContext{Stats: stats, Effect: combat.Effect{SkillID: 3221007, Damage: 720, MobCount: 1}, Mob: mob}
	base := combat.LineCeiling(dc)
	// Snipe follows the ordinary bound on every mob; the client patch agrees.
	if attackSpec(t, 3221007).MaxDamage != nil {
		t.Errorf("snipe keeps its own ceiling")
	}
	dc.Effect.MobCount = 3
	if got := attackSpec(t, 3221001).MaxDamage(dc); got != min(3*base, combat.MaxLineDamage()) {
		t.Errorf("piercing %d", got)
	}
	strafe := attackSpec(t, 3211006).MaxDamage
	if got := strafe(dc); got != base {
		t.Errorf("strafe %d", got)
	}
	dc.Statuses = combat.MobStatuses{combat.MobFreeze: {Stat: combat.MobFreeze, SkillID: 3211003}}
	if got := strafe(dc); got != combat.MaxLineDamage() {
		t.Errorf("strafe on a blizzard-frozen mob %d", got)
	}
}

func TestSummons(t *testing.T) {
	for _, tc := range []struct {
		id      int
		stat    buffstat.Stat
		move    uint8
		attacks bool
		puppet  bool
	}{
		{3101007, "SUMMON", 4, true, false}, {3201007, "SUMMON", 4, true, false},
		{3111005, "SUMMON", 4, true, false}, {3211005, "SUMMON", 4, true, false},
		{3111002, "PUPPET", 0, false, true}, {3211002, "PUPPET", 0, false, true},
		{3120012, "PUPPET", 0, true, true}, {3220012, "PUPPET", 0, true, true},
	} {
		s := attackSpec(t, tc.id)
		if s.Summon == nil || s.Summon.MoveAbility != tc.move || s.Summon.Puppet != tc.puppet || s.Allows(combat.Summoned) != tc.attacks || s.Allows(combat.Shoot) {
			t.Errorf("summon %d spec %+v %+v", tc.id, s, s.Summon)
		}
		p := mustCast(t, tc.id, 1, archer(0))
		if p.Self == nil || len(p.Self.Stats) != 1 || p.Self.Stats[0] != (skills.StatValue{Stat: tc.stat, Value: 1}) || p.Self.Duration <= 0 {
			t.Errorf("summon %d cast %+v", tc.id, p.Self)
		}
	}
}
