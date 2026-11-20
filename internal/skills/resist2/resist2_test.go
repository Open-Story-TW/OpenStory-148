package resist2

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

var now = time.Unix(1000, 0)

// pending lists any R2 skills that still wait for core support.
var pending = map[int]bool{}

func TestSG88KeepsOperatorAndRobotAttacksSeparate(t *testing.T) {
	const id = 35121003
	t.Setenv("MAPLEGO_WZ_ROOT", "")
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	body, err := combat.FromSkill(def, 30)
	if err != nil {
		t.Fatal(err)
	}
	ch := domain.Character{Job: 3512, X: 100, Y: 200}
	ctx := combat.AttackContext{Kind: combat.Body, Character: ch, Effect: body, Now: now,
		Attack: protocol.Attack{X: 350, Y: 350}, Targets: []combat.TargetInfo{{X: 240, Y: 200}}}
	h, _ := skills.Lookup(id)
	if !h.Attack.Allows(combat.Body) || !h.Attack.Allows(combat.Summoned) || h.Attack.Allows(combat.Melee) {
		t.Fatalf("invalid native SG-88 attack families: %+v", h.Attack)
	}
	values, err := def.Values(30)
	if err != nil {
		t.Fatal(err)
	}
	cast, err := h.Cast(&skills.CastContext{Skill: def, Level: 30, Values: values, Caster: ch, Now: now})
	if err != nil || cast.Self == nil || cast.Self.Duration != 6*time.Second || h.Attack.Summon.Duration != cast.Self.Duration || h.DropOnEnter == nil || !h.DropOnEnter(ch) {
		t.Fatalf("SG-88 activation escaped its native visit or orphan deadline: %+v (%v)", cast, err)
	}
	if err := h.Attack.Prepare(&ctx); err != nil || ctx.Effect.Damage != 250 || ctx.Effect.MobCount != 1 || ctx.Effect.AttackCount != 1 || ctx.Attack.X != 100 || ctx.Attack.Y != 200 {
		t.Fatalf("SG-88 operator attack: %+v, %v", ctx.Effect, err)
	}
	ctx.Targets[0].X = 246
	if err := h.Attack.Prepare(&ctx); err != combat.ErrRange {
		t.Fatalf("SG-88 accepted operator hit outside body range: %v", err)
	}
	robot, err := combat.SummonEffect(def, 30)
	if err != nil || robot.Damage != 1440 || robot.MobCount != 15 || robot.AttackCount != 1 || robot.Box.LT != (combat.Point{X: -400, Y: -350}) || robot.Box.RB != (combat.Point{X: 400, Y: 250}) {
		t.Fatalf("SG-88 robot attack: %+v, %v", robot, err)
	}
	if _, _, err := skills.AttackResourceCost(ch, id, 30, protocol.RecvBodyAttack, now); err == nil {
		t.Fatal("SG-88 operator attack accepted before its activation was paid")
	}
	ch.SkillBuffs = []domain.SkillBuff{row(id, "COMBO", 1)}
	if cost, ongoing, err := skills.AttackResourceCost(ch, id, 30, protocol.RecvBodyAttack, now); err != nil || !ongoing || cost.MP != 0 || cost.Cooldown != 0 {
		t.Fatalf("SG-88 charged operator attack after its paid cast: %+v, %t, %v", cost, ongoing, err)
	}
}

func TestRegistersInventorySkills(t *testing.T) {
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
	if err := json.Unmarshal(raw, &inv); err != nil {
		t.Fatal(err)
	}
	var want, got []int
	total := 0
	for _, s := range inv.Skills {
		if s.Thread != "R2" {
			continue
		}
		total++
		if !pending[s.ID] {
			want = append(want, s.ID)
		}
	}
	for _, id := range skills.IDs() {
		if h, _ := skills.Lookup(id); h.Package() == "resist2" {
			got = append(got, id)
		}
	}
	sort.Ints(want)
	if total != 71 || !reflect.DeepEqual(got, want) {
		t.Fatalf("registered %d %v\ninventory %d (want %d) %v", len(got), got, total, len(want), want)
	}
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

func caster() domain.Character {
	return domain.Character{Job: 3512, HP: 5000, MaxHP: 5000, MP: 5000, MaxMP: 5000}
}

func castWith(t *testing.T, c domain.Character, id, level int) (skills.CastPlan, error) {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, v := values(t, id, level)
	return h.Cast(&skills.CastContext{Skill: def, Level: level, Values: v, Caster: c, Now: now})
}

func cast(t *testing.T, id, level int) skills.CastPlan {
	t.Helper()
	p, err := castWith(t, caster(), id, level)
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func row(id int, stat string, value int) domain.SkillBuff {
	return domain.SkillBuff{ID: id, Level: 1, Stat: stat, Value: value, ExpiresAt: now.Add(time.Minute)}
}

func stats(b *skills.Buff) map[buffstat.Stat]int {
	out := map[buffstat.Stat]int{}
	for _, s := range b.Stats {
		out[s.Stat] = s.Value
	}
	return out
}

// Every castable skill plans at every level with sendable buff stats, and
// every attack has damage.
func TestEveryHandlerPlansKnownStats(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 3 }
	for _, id := range skills.IDs() {
		if skills.OwnerOf(id) != "resist2" {
			continue
		}
		h, _ := skills.Lookup(id)
		def, err := content.SkillDefinition("", id)
		if err != nil {
			t.Fatalf("%d: %v", id, err)
		}
		// 33121005 has no WZ damage; combat.FromSkill uses 100 like Java.
		if a := h.Attack; a != nil && a.Kind != 0 && a.Kind != combat.Summoned && id != 33121005 {
			if _, v := values(t, id, def.MaxLevel); v["damage"] < 1 {
				t.Errorf("%d: attack without damage", id)
			}
		}
		if h.Cast == nil {
			continue
		}
		c := caster()
		if id == 33101008 {
			c.SkillBuffs = []domain.SkillBuff{row(33101004, "RAINING_MINES", 1)}
		}
		for level := 1; level <= def.MaxLevel; level++ {
			p, err := castWith(t, c, id, level)
			if err != nil {
				t.Errorf("%d@%d: %v", id, level, err)
				continue
			}
			if p.MPCost < 0 || p.Cooldown < 0 {
				t.Errorf("%d@%d: negative cost %+v", id, level, p)
			}
			for _, b := range []*skills.Buff{p.Self, p.Party} {
				if b == nil {
					continue
				}
				if b.Duration <= 0 || b.Duration > skills.Permanent {
					t.Errorf("%d@%d: duration %v", id, level, b.Duration)
				}
				var names []buffstat.Stat
				for _, s := range b.Stats {
					names = append(names, s.Stat)
				}
				if _, err := buffstat.Resolve(names); err != nil {
					t.Errorf("%d@%d: %v", id, level, err)
				}
			}
		}
	}
}

func TestJaguarRiding(t *testing.T) {
	p := cast(t, 33001001, 15)
	if !p.Toggle || p.Self.Duration != skills.Permanent || stats(p.Self)["MONSTER_RIDING"] != jaguarMount || p.MPCost != 20 {
		t.Fatalf("plan %+v %+v", p, p.Self)
	}
	h, _ := skills.Lookup(33001001)
	def, v := values(t, 33001001, 15)
	c := caster()
	ctx := &skills.PassiveContext{Skill: def, Level: 15, Values: v, Character: c, Now: now}
	if b := h.Passive(ctx); b != (skills.Bonus{}) {
		t.Fatalf("bonus without riding: %+v", b)
	}
	ctx.Character.SkillBuffs = []domain.SkillBuff{row(33001001, "MONSTER_RIDING", jaguarMount)}
	if b := h.Passive(ctx); b.Critical != 40 || b.MaxHPPercent != 40 {
		t.Fatalf("riding bonus %+v", b)
	}
}

func TestMechUsesExtremeMechStats(t *testing.T) {
	p := cast(t, 35001002, 10)
	if s := stats(p.Self); s["MONSTER_RIDING"] != mechMount || s["ENHANCED_WATK"] != 20 || s["ENHANCED_MAXHP"] != 500 || s["ANGEL_SPEED"] != 20 || !p.Toggle {
		t.Fatalf("prototype %+v", s)
	}
	c := caster()
	c.Skills = []domain.SkillState{{ID: extremeMech, Level: 30}}
	p, err := castWith(t, c, 35001002, 10)
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(p.Self); s["ENHANCED_WATK"] != 40 || s["ENHANCED_WDEF"] != 1000 || s["ENHANCED_MAXHP"] != 1200 || s["ANGEL_SPEED"] != 0 || p.MPCost != 50 {
		t.Fatalf("extreme mech %+v %+v", s, p)
	}
}

func TestMineNeedsMineBuff(t *testing.T) {
	if _, err := castWith(t, caster(), 33101008, 20); err == nil {
		t.Fatal("mine without RAINING_MINES")
	}
	c := caster()
	c.SkillBuffs = []domain.SkillBuff{row(33101004, "RAINING_MINES", 1), row(33111005, "SUMMON", 1)}
	if _, err := castWith(t, c, 33101008, 20); err == nil {
		t.Fatal("mine while another summon is up")
	}
	c.SkillBuffs = c.SkillBuffs[:1]
	p, err := castWith(t, c, 33101008, 20)
	if err != nil || p.Self.Duration != 2*time.Second || stats(p.Self)["SUMMON"] != 1 {
		t.Fatalf("mine %+v %v", p, err)
	}
}

func TestBuffValues(t *testing.T) {
	for _, tc := range []struct {
		id, level int
		want      map[buffstat.Stat]int
		duration  time.Duration
	}{
		{33001003, 20, map[buffstat.Stat]int{"BOOSTER": -2}, 200 * time.Second},
		{33101003, 10, map[buffstat.Stat]int{"SOULARROW": 1}, 300 * time.Second},
		{33101004, 20, map[buffstat.Stat]int{"RAINING_MINES": 1}, 100 * time.Second},
		{33111004, 10, map[buffstat.Stat]int{"BLIND": 20}, 160 * time.Second},
		{33111007, 30, map[buffstat.Stat]int{"SPEED": 20, "DAMAGE_BUFF": 15, "ATTACK_BUFF": 80}, 120 * time.Second},
		{35101006, 20, map[buffstat.Stat]int{"BOOSTER": -2}, 200 * time.Second},
		{35101007, 20, map[buffstat.Stat]int{"PERFECT_ARMOR": 500}, skills.Permanent},
		{35121006, 25, map[buffstat.Stat]int{"SATELLITESAFE_PROC": 30, "SATELLITESAFE_ABSORB": 80}, skills.Permanent},
		{35111004, 20, map[buffstat.Stat]int{"MECH_CHANGE": 20}, skills.Permanent},
		{35121005, 30, map[buffstat.Stat]int{"MECH_CHANGE": 30}, skills.Permanent},
		{35121010, 1, map[buffstat.Stat]int{"DAMAGE_BUFF": 1}, 42 * time.Second},
		{35121010, 20, map[buffstat.Stat]int{"DAMAGE_BUFF": 20}, 80 * time.Second},
	} {
		p := cast(t, tc.id, tc.level)
		if got := stats(p.Self); !reflect.DeepEqual(got, tc.want) || p.Self.Duration != tc.duration {
			t.Errorf("%d: %v %v, want %v %v", tc.id, got, p.Self.Duration, tc.want, tc.duration)
		}
	}
	if p := cast(t, 33111007, 30); p.Cooldown != 150*time.Second {
		t.Errorf("feline berserk cooldown %v", p.Cooldown)
	}
}

func TestPartyBuffs(t *testing.T) {
	p := cast(t, 33121004, 30)
	if p.Party == nil || stats(p.Party)["SHARP_EYES"] != 20<<8|30 {
		t.Fatalf("sharp eyes %+v", p)
	}
	for _, id := range []int{33121007, 35121007} {
		if p := cast(t, id, 30); p.Party == nil || stats(p.Party)["MAPLE_WARRIOR"] != 15 || p.Party.Duration != 900*time.Second {
			t.Errorf("%d: %+v", id, p)
		}
	}
}

func TestDice(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 1 }
	if p := cast(t, 35111013, 20); p.Self != nil || p.MPCost != 60 || p.Cooldown != 200*time.Second {
		t.Fatalf("roll 1: %+v", p)
	}
	rollDie = func(int) int { return 4 }
	if p := cast(t, 35111013, 20); stats(p.Self)["DICE_ROLL"] != 4 {
		t.Fatalf("roll 4: %+v", p.Self)
	}
}

func TestSummonDurations(t *testing.T) {
	if p := cast(t, 35111001, 20); p.Self.Duration != skills.Permanent || stats(p.Self)["PUPPET"] != 1 {
		t.Fatalf("satellite %+v", p.Self)
	}
	if p := cast(t, 33111005, 20); p.Self.Duration != 180*time.Second || stats(p.Self)["SUMMON"] != 1 {
		t.Fatalf("silver hawk %+v", p.Self)
	}
	c := caster()
	c.Skills = []domain.SkillState{{ID: robotMastery, Level: 15}}
	p, err := castWith(t, c, 35111011, 20)
	if err != nil || p.Self.Duration != 45*time.Second {
		t.Fatalf("healing robot with robot mastery: %+v %v", p.Self, err)
	}
}

func TestPassives(t *testing.T) {
	bonus := func(c domain.Character, id, level int) skills.Bonus {
		h, _ := skills.Lookup(id)
		def, v := values(t, id, level)
		return h.Passive(&skills.PassiveContext{Skill: def, Level: level, Values: v, Character: c, Now: now})
	}
	c := caster()
	if b := bonus(c, 35100000, 20); b.WATK != 20 || b.Mastery != 0 {
		t.Errorf("mechanic mastery without gun %+v", b)
	}
	c.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1492000}}
	if b := bonus(c, 35100000, 20); b.WATK != 20 || b.Mastery != 50 || b.ACC != 120 || b.Critical != 10 {
		t.Errorf("mechanic mastery %+v", b)
	}
	extreme := c
	extreme.Skills = []domain.SkillState{{ID: extremeMech, Level: 30}}
	if b := bonus(extreme, 35100000, 20); b.Mastery != 0 || b.ACC != 120 || b.Critical != 10 {
		t.Errorf("mechanic mastery with Extreme Mech %+v", b)
	}
	if b := bonus(extreme, extremeMech, 30); b.Mastery != 70 {
		t.Errorf("extreme mech mastery %+v", b)
	}
	c.Equipment = []domain.Equipment{{Slot: -11, ItemID: 1462000}}
	if b := bonus(c, 33100000, 20); b.Mastery != 50 || b.ACC != 120 {
		t.Errorf("crossbow mastery %+v", b)
	}
	// Crossbow Expert sets the mastery instead of adding to the basic one.
	expert := c
	expert.Job = 3312 // only a fourth job Wild Hunter can hold Crossbow Expert
	expert.Skills = append(append([]domain.SkillState(nil), c.Skills...), domain.SkillState{ID: 33120000, Level: 30})
	if basic, adv := bonus(expert, 33100000, 20), bonus(expert, 33120000, 30); basic.Mastery != 0 || basic.ACC != 120 || adv.Mastery != 70 {
		t.Errorf("crossbow expert replaces mastery: basic %+v expert %+v", basic, adv)
	}
	if b := bonus(c, 33110000, 20); b.DamagePercent != 40 || b.BossDamagePercent != 40 {
		t.Errorf("jaguar mastery %+v", b)
	}
	if b := bonus(c, 33120000, 30); b.WATK != 30 || b.Mastery != 70 || b.CriticalDamageMin != 15 {
		t.Errorf("crossbow expert %+v", b)
	}
	if b := bonus(c, 33120010, 10); b.IgnoreDefense != 20 || b.AvoidRate != 10 {
		t.Errorf("wild instinct %+v", b)
	}
	if b := bonus(c, 35111004, 20); b.Critical != 0 {
		t.Errorf("siege crit outside the mode %+v", b)
	}
	c.SkillBuffs = []domain.SkillBuff{row(35111004, "MECH_CHANGE", 20)}
	if b := bonus(c, 35111004, 20); b.Critical != 100 {
		t.Errorf("siege crit %+v", b)
	}
	c.SkillBuffs = nil
	if b := bonus(c, 33111007, 30); b.DamagePercent != 0 {
		t.Errorf("feline berserk inactive %+v", b)
	}
	c.SkillBuffs = []domain.SkillBuff{row(33111007, "DAMAGE_BUFF", 15)}
	if b := bonus(c, 33111007, 30); b.DamagePercent != 15 || b.BossDamagePercent != 15 {
		t.Errorf("feline berserk active %+v", b)
	}
}

func TestAttackPrepare(t *testing.T) {
	h, _ := skills.Lookup(35111004)
	ctx := &combat.AttackContext{Character: caster(), Effect: combat.Effect{MobCount: 1, AttackCount: 1, BulletCount: 2}, Now: now}
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.MobCount != 6 || ctx.Effect.AttackCount != 6 || ctx.Effect.BulletCount != 15 {
		t.Fatalf("siege %+v %v", ctx.Effect, err)
	}
	if h.Attack.Ammo != combat.AmmoNone {
		t.Fatal("mechanic attacks consume no bullets")
	}
	h, _ = skills.Lookup(35001003)
	c := caster()
	c.Job = 3511
	c.Skills = []domain.SkillState{{ID: metalFist, Level: 20}}
	ctx = &combat.AttackContext{Character: c, Effect: combat.Effect{Damage: 180}, Now: now}
	if err := h.Attack.Prepare(ctx); err != nil || ctx.Effect.Damage != 270 {
		t.Fatalf("metal fist %+v %v", ctx.Effect, err)
	}
	h, _ = skills.Lookup(33111001)
	c = caster()
	c.SkillBuffs = []domain.SkillBuff{row(33101003, "SOULARROW", 1)}
	ctx = &combat.AttackContext{Character: c, Now: now}
	if err := h.Attack.Prepare(ctx); err != nil || !ctx.NoAmmo {
		t.Fatal("soul arrow shoots without arrows")
	}
}

// Exploding Arrows' last line is the x% explosion (v148 0x712B62); the arrows keep damage%.
func TestExplodingArrowsExplosionLine(t *testing.T) {
	h, _ := skills.Lookup(33121001)
	f := h.Attack.LineDamage
	e := combat.Effect{SkillID: 33121001, Damage: 180, X: 850, AttackCount: 11}
	if f == nil || f(e, 0, 11) != 0 || f(e, 9, 11) != 0 || f(e, 10, 11) != 850 {
		t.Fatal("explosion line percent")
	}
}

func TestCeilings(t *testing.T) {
	stats := combat.PlayerStats{Job: 3512, STR: 100, DEX: 500, WATK: 100, WeaponID: 1492000}
	mob := content.Mob{HP: 100000}
	bomb := combat.DamageContext{Stats: stats, Mob: mob, Effect: combat.Effect{SkillID: 33101001, Level: 20, Damage: 360, MobCount: 8}}
	if got, base := bombCeiling(bomb), combat.LineCeiling(bomb); got != min(base*8, combat.MaxLineDamage()) || base < 1 {
		t.Fatalf("arrow bomb %d base %d", got, base)
	}
	c := caster()
	c.Skills = []domain.SkillState{{ID: robotMastery, Level: 15}}
	bot := combat.DamageContext{Stats: stats, Mob: mob, Attacker: c, Effect: combat.Effect{SkillID: 35121011, Level: 30}}
	want := bot
	want.Effect.Damage = 350 * 150 / 100
	if got := boostedSummon(robotMastery, "x")(bot); got != combat.SummonCeiling(want) {
		t.Fatalf("bot ceiling %d want %d", got, combat.SummonCeiling(want))
	}
}

func TestHealingRobot(t *testing.T) {
	_, v := values(t, 35111011, 20)
	c := combat.SubSummonContext{Character: caster(), SubSkill: 35111011, SubValues: v, LastSub: now.Add(-2 * time.Second), Now: now}
	if r, err := healingRobot(c); err != nil || r.HealHP != 2500 {
		t.Fatalf("heal %+v %v", r, err)
	}
	c.LastSub = now.Add(-500 * time.Millisecond)
	if _, err := healingRobot(c); err == nil {
		t.Fatal("heal twice within a second")
	}
}

func TestLinkedLevels(t *testing.T) {
	for id, from := range map[int]int{33101007: 33101005, 35101009: heavyWeapon, 35101010: heavyWeapon, 35121013: 35111004} {
		if h, _ := skills.Lookup(id); h.Attack.LevelFrom != from {
			t.Errorf("%d: attack LevelFrom %d, want %d", id, h.Attack.LevelFrom, from)
		}
	}
	for id, from := range map[int]int{33101006: 33101005, 33101008: 33101004, 35111009: 35111001, 35111010: 35111001, 35121013: 35111004} {
		if h, _ := skills.Lookup(id); h.LevelFrom != from {
			t.Errorf("%d: cast LevelFrom %d, want %d", id, h.LevelFrom, from)
		}
	}
}

// The enhanced Flame Launcher and Gatling take Heavy Weapon Mastery's cr.
func TestHeavyWeaponCritical(t *testing.T) {
	c := caster()
	c.Skills = []domain.SkillState{{ID: heavyWeapon, Level: 20}}
	cr := skillValues(heavyWeapon, 20)["cr"]
	for _, id := range []int{35101009, 35101010} {
		h, _ := skills.Lookup(id)
		ctx := &combat.AttackContext{Character: c, Effect: combat.Effect{SkillID: id, CriticalRate: 0}, Now: now}
		if err := h.Attack.Prepare(ctx); err != nil || cr < 1 || ctx.Effect.CriticalRate != cr {
			t.Errorf("%d critical rate %d want %d (%v)", id, ctx.Effect.CriticalRate, cr, err)
		}
	}
	ctx := &combat.AttackContext{Character: caster(), Effect: combat.Effect{SkillID: 35101010}, Now: now}
	if h, _ := skills.Lookup(35101010); h.Attack.Prepare(ctx) != nil || ctx.Effect.CriticalRate != 0 {
		t.Errorf("gatling without Heavy Weapon Mastery %d", ctx.Effect.CriticalRate)
	}
}

func TestSwallowBuff(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	want := []map[buffstat.Stat]int{
		{"CRITICAL_RATE_BUFF": 10}, {"MP_BUFF": 10}, {"DAMAGE_TAKEN_BUFF": 40},
		{"DODGE_CHANGE_BUFF": 40}, {"DAMAGE_BUFF": 10}, {"ATTACK_BUFF": 10},
	}
	for i, w := range want {
		rollDie = func(int) int { return i + 1 }
		if p := cast(t, 33101006, 20); !reflect.DeepEqual(stats(p.Self), w) || p.Self.Duration != 170*time.Second {
			t.Errorf("roll %d: %v", i+1, stats(p.Self))
		}
	}
	h, _ := skills.Lookup(33101006)
	def, v := values(t, 33101006, 20)
	c := caster()
	c.SkillBuffs = []domain.SkillBuff{row(33101006, "CRITICAL_RATE_BUFF", 10), row(33101006, "DAMAGE_BUFF", 10)}
	if b := h.Passive(&skills.PassiveContext{Skill: def, Level: 20, Values: v, Character: c, Now: now}); b.Critical != 10 || b.DamagePercent != 10 || b.BossDamagePercent != 10 {
		t.Fatalf("swallow bonus %+v", b)
	}
}

func trigger(t *testing.T, id, level int, c domain.Character) *skills.TriggerContext {
	t.Helper()
	def, v := values(t, id, level)
	var rows []domain.SkillBuff
	for _, r := range c.SkillBuffs {
		if r.ID == id {
			rows = append(rows, r)
		}
	}
	return &skills.TriggerContext{Skill: def, Level: level, Values: v, Character: c, Rows: rows, Now: now}
}

func TestMissileTankUpkeep(t *testing.T) {
	c := caster()
	c.Skills = []domain.SkillState{{ID: missileTank, Level: 30}, {ID: 35001002, Level: 10}}
	c.SkillBuffs = []domain.SkillBuff{row(missileTank, "MECH_CHANGE", 30), row(35001002, "MONSTER_RIDING", mechMount)}
	tank, _ := skills.Lookup(missileTank)
	mech, _ := skills.Lookup(35001002)
	if tank.TickEvery != 5*time.Second || mech.TickEvery != 5*time.Second {
		t.Fatal("upkeep ticks every 5 seconds")
	}
	if ch := tank.OnTick(trigger(t, missileTank, 30, c)); ch == nil || ch.CostMP != 25 || ch.Cancel {
		t.Fatalf("tank upkeep %+v", ch)
	}
	if ch := mech.OnTick(trigger(t, 35001002, 10, c)); ch != nil {
		t.Fatalf("mech pays nothing %+v", ch)
	}
	c.MP = 24
	for _, h := range []struct {
		h  skills.Handler
		id int
		lv int
	}{{tank, missileTank, 30}, {mech, 35001002, 10}} {
		if ch := h.h.OnTick(trigger(t, h.id, h.lv, c)); ch == nil || !ch.Cancel {
			t.Errorf("%d without MP: %+v", h.id, ch)
		}
	}
	c.SkillBuffs = []domain.SkillBuff{row(35111004, "MECH_CHANGE", 20), row(35001002, "MONSTER_RIDING", mechMount)}
	if ch := mech.OnTick(trigger(t, 35001002, 10, c)); ch != nil {
		t.Fatalf("siege mode has no upkeep %+v", ch)
	}
}

func TestSatelliteSafetyCostsOneSatellite(t *testing.T) {
	c := caster()
	c.SkillBuffs = []domain.SkillBuff{row(satelliteSafe, "SATELLITESAFE_PROC", 30), row(35111001, "PUPPET", 1), row(35111009, "PUPPET", 1)}
	first, _ := skills.Lookup(35111001)
	second, _ := skills.Lookup(35111009)
	ctx := trigger(t, 35111001, 20, c)
	ctx.Damage = 1499
	if first.OnDamaged(ctx) != nil {
		t.Fatal("hit below 30% of MaxHP")
	}
	ctx.Damage = 1500
	if ch := first.OnDamaged(ctx); ch == nil || !ch.Cancel {
		t.Fatalf("first satellite %+v", ch)
	}
	ctx2 := trigger(t, 35111009, 20, c)
	ctx2.Damage = 1500
	if second.OnDamaged(ctx2) != nil {
		t.Fatal("only one satellite per hit")
	}
	c.SkillBuffs = c.SkillBuffs[1:]
	ctx = trigger(t, 35111001, 20, c)
	ctx.Damage = 5000
	if first.OnDamaged(ctx) != nil {
		t.Fatal("no Satellite Safety")
	}
}

func TestPerfectArmorReflects(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	h, _ := skills.Lookup(35101007)
	def, v := values(t, 35101007, 20)
	ctx := &skills.TriggerContext{Skill: def, Level: 20, Values: v, Character: caster(), Damage: 300, Mob: 7,
		Mobs: []skills.TriggerMob{{ObjectID: 7, MaxHP: 100000}}, Now: now}
	if h.OnDamaged(ctx) != nil {
		t.Fatal("reflects without Perfect Armor")
	}
	ctx.Rows = []domain.SkillBuff{row(35101007, "PERFECT_ARMOR", 500)}
	rollDie = func(int) int { return 21 }
	if h.OnDamaged(ctx) != nil {
		t.Fatal("reflects outside prop")
	}
	rollDie = func(int) int { return 20 }
	if ch := h.OnDamaged(ctx); ch == nil || len(ch.MobHits) != 1 || ch.MobHits[0] != (combat.ServerHit{ObjectID: 7, Damage: 1500}) {
		t.Fatalf("reflection %+v", ch)
	}
	ctx.Damage = 5000
	if ch := h.OnDamaged(ctx); ch.MobHits[0].Damage != 10000 {
		t.Fatalf("cap maxHP/10 %+v", ch.MobHits)
	}
}

func TestMechModeAttacks(t *testing.T) {
	siege, _ := skills.Lookup(35111004)
	c := caster()
	if siege.Attack.Remap(c, now) != 0 {
		t.Fatal("siege outside the tank stays 35111004")
	}
	c.SkillBuffs = []domain.SkillBuff{row(missileTank, "MECH_CHANGE", 30)}
	if siege.Attack.Remap(c, now) != 35121013 {
		t.Fatal("siege inside the tank is 35121013")
	}
	for _, id := range []int{35111004, 35121013} {
		if h, _ := skills.Lookup(id); !h.Attack.NoCooldown {
			t.Errorf("%d: mode attack starts a cooldown", id)
		}
	}
}

func TestSummonSpecs(t *testing.T) {
	acc, _ := skills.Lookup(35111005)
	if m := acc.Attack.MobCast; m == nil || !m.WholeField || !m.SkipBoss {
		t.Fatalf("accelerator %+v", m)
	}
	for _, id := range []int{33101008, 35121011} {
		h, _ := skills.Lookup(id)
		if s := h.Attack.Summon; !s.SingleAttack || !s.Multi || h.Attack.Kind != combat.Summoned {
			t.Errorf("%d: self-destructing summon %+v", id, s)
		}
	}
	bot, _ := skills.Lookup(35121011)
	if bot.LevelFrom != 35121009 || bot.Attack.LevelFrom != 35121009 {
		t.Fatal("bots use the factory's level")
	}
}

func TestRobotFactorySpawnsThreeBots(t *testing.T) {
	c := combat.SubSummonContext{Character: caster(), SummonSkill: 35121009, SummonLvl: 25, SubSkill: 35121009, LastSub: now.Add(-3 * time.Second), Now: now}
	r, err := robotFactory(c)
	if err != nil || len(r.Spawn) != 3 || r.Spawn[0] != (combat.SummonChild{SkillID: 35121011, Level: 25}) {
		t.Fatalf("factory %+v %v", r, err)
	}
	c.LastSub = now.Add(-time.Second)
	if _, err := robotFactory(c); err == nil {
		t.Fatal("factory twice within two seconds")
	}
}

// Java's permanent summons have no WZ time; the spawn needs an explicit one.
func TestPermanentSummonsCarryDuration(t *testing.T) {
	for _, id := range []int{35111001, 35111009, 35111010} {
		if h, _ := skills.Lookup(id); h.Attack.Summon.Duration != skills.Permanent {
			t.Errorf("%d: summon duration %v", id, h.Attack.Summon.Duration)
		}
	}
}

func TestSiegeInMissileTankBecomesSiege2(t *testing.T) {
	p := cast(t, 35111004, 20)
	if p.ApplyAs != 0 || p.Toggle || stats(p.Self)["MECH_CHANGE"] != 20 {
		t.Fatalf("plain siege %+v", p)
	}
	c := caster()
	c.SkillBuffs = []domain.SkillBuff{row(missileTank, "MECH_CHANGE", 30)}
	p, err := castWith(t, c, 35111004, 20)
	if err != nil || p.ApplyAs != siege2 || p.Toggle || stats(p.Self)["MECH_CHANGE"] != 20 {
		t.Fatalf("siege in the tank %+v %v", p, err)
	}
	c.SkillBuffs = []domain.SkillBuff{row(siege2, "MECH_CHANGE", 20)}
	if p, err = castWith(t, c, 35111004, 20); err != nil || p.ApplyAs != 0 {
		t.Fatalf("siege in siege 2 is plain siege (Java) %+v %v", p, err)
	}
	h, _ := skills.Lookup(siege2)
	var applied []int
	defer skills.SetBuffApplier(nil)
	skills.SetBuffApplier(func(owner int64, ch domain.SkillBuffChange) { applied = append(applied, ch.SkillID) })
	c = caster()
	c.Skills = []domain.SkillState{{ID: missileTank, Level: 30}}
	h.OnEnd(&skills.EndContext{SkillID: siege2, Level: 20, Character: c, Now: now})
	c.SkillBuffs = []domain.SkillBuff{row(35111004, "MECH_CHANGE", 20)} // overwritten by Siege Mode
	h.OnEnd(&skills.EndContext{SkillID: siege2, Level: 20, Character: c, Now: now})
	if len(applied) != 1 || applied[0] != missileTank {
		t.Fatalf("restored %v", applied)
	}
}

// Sonic Roar (33121002) routes through the v148 melee sender (0x91A08F) and
// consumes no arrows while applying its stun on hit.
func TestSonicRoarIsMeleeNoAmmo(t *testing.T) {
	h, ok := skills.Lookup(33121002)
	if !ok || h.Attack == nil || h.Attack.Kind != combat.Melee || h.Attack.Ammo != combat.AmmoNone || len(h.Attack.OnHit) == 0 {
		t.Fatalf("33121002 handler: %+v", h.Attack)
	}
}
