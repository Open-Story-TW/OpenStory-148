package magician

import (
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Skills of thread M not registered yet, with the framework they wait for.
var pending = map[int]string{
	2000007: "invisible Element Weaken proc",
}

func level(t *testing.T, id, lv int) (content.Skill, map[string]int) {
	t.Helper()
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(lv)
	if err != nil {
		t.Fatal(err)
	}
	return def, v
}

func cast(t *testing.T, id, lv int) skills.CastPlan {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, v := level(t, id, lv)
	p, err := h.Cast(&skills.CastContext{Skill: def, Level: lv, Values: v, Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

// Every enabled skill of jobs 200-232 is registered or listed as pending.
func TestEveryMagicianSkillIsRegisteredOrPending(t *testing.T) {
	for job := 200; job <= 232; job++ {
		for id := job * 10000; id < job*10000+10000; id++ {
			def, err := content.SkillDefinition("", id)
			if err != nil || def.Disabled {
				continue
			}
			_, registered := skills.Lookup(id)
			if _, wait := pending[id]; registered == wait {
				t.Errorf("skill %d registered=%v pending=%v", id, registered, wait)
			}
		}
	}
}

func TestBuffPlansFollowJava(t *testing.T) {
	sv := func(stat buffstat.Stat, v int) skills.StatValue { return skills.StatValue{Stat: stat, Value: v} }
	for _, c := range []struct {
		id, lv   int
		party    bool
		mp       int
		cooldown time.Duration
		duration time.Duration
		stats    []skills.StatValue
	}{
		{2001002, 15, false, 13, 0, 600 * time.Second, []skills.StatValue{sv("MAGIC_GUARD", 85)}},
		{2001003, 15, false, 16, 0, 240 * time.Second, []skills.StatValue{sv("WDEF", 120), sv("MDEF", 225)}},
		{2101001, 20, true, 16, 0, 200 * time.Second, []skills.StatValue{sv("MATK", 30)}},
		{2201001, 20, true, 16, 0, 200 * time.Second, []skills.StatValue{sv("MATK", 30)}},
		{2111005, 20, false, 20, 0, 200 * time.Second, []skills.StatValue{sv("BOOSTER", -2)}},
		{2211005, 20, false, 20, 0, 200 * time.Second, []skills.StatValue{sv("BOOSTER", -2)}},
		{2311006, 20, false, 20, 0, 200 * time.Second, []skills.StatValue{sv("BOOSTER", -2)}},
		{2111008, 10, false, 50, 0, 300 * time.Second, []skills.StatValue{sv("ELEMENT_RESET", 100)}},
		{2211008, 10, false, 50, 0, 300 * time.Second, []skills.StatValue{sv("ELEMENT_RESET", 100)}},
		{2111007, 10, false, 20, 0, skills.Permanent, []skills.StatValue{sv("TELEPORT_MASTERY", 20)}},
		{2211007, 10, false, 20, 0, skills.Permanent, []skills.StatValue{sv("TELEPORT_MASTERY", 20)}},
		{2311007, 10, false, 20, 0, skills.Permanent, []skills.StatValue{sv("TELEPORT_MASTERY", 20)}},
		{2121000, 30, true, 70, 0, 900 * time.Second, []skills.StatValue{sv("MAPLE_WARRIOR", 15)}},
		{2221000, 30, true, 70, 0, 900 * time.Second, []skills.StatValue{sv("MAPLE_WARRIOR", 15)}},
		{2321000, 30, true, 70, 0, 900 * time.Second, []skills.StatValue{sv("MAPLE_WARRIOR", 15)}},
		{2121004, 30, false, 80, 180 * time.Second, 40 * time.Second, []skills.StatValue{sv("INFINITY", 1), sv("STANCE", 70)}},
		{2221004, 30, false, 80, 180 * time.Second, 40 * time.Second, []skills.StatValue{sv("INFINITY", 1), sv("STANCE", 50)}},
		{2321004, 30, false, 80, 180 * time.Second, 40 * time.Second, []skills.StatValue{sv("INFINITY", 1), sv("STANCE", 50)}},
		{2301003, 15, false, 30, 0, 300 * time.Second, []skills.StatValue{sv("INVINCIBLE", 20)}},
		{2301004, 15, true, 25, 0, 240 * time.Second, []skills.StatValue{sv("BLESS", 15)}},
		{2311003, 20, true, 300, 0, 180 * time.Second, []skills.StatValue{sv("HOLY_SYMBOL", 50)}},
		{2311009, 20, true, 70, 30 * time.Second, 20 * time.Second, []skills.StatValue{sv("HOLY_MAGIC_SHELL", 10)}},
		{2321005, 30, true, 58, 0, 240 * time.Second, []skills.StatValue{sv("HOLY_SHIELD", 25), sv("HP_BOOST", 700), sv("MP_BOOST", 700)}},
	} {
		p := cast(t, c.id, c.lv)
		b := p.Self
		if c.party {
			b = p.Party
		}
		if c.party && (p.Area == nil || p.Area.Left >= 0 || p.Area.Right <= 0) {
			t.Errorf("skill %d: party area %+v", c.id, p.Area)
		}
		if toggle := c.id%10000 == 1007; p.Toggle != toggle {
			t.Errorf("skill %d: toggle %v", c.id, p.Toggle)
		}
		if b == nil || (p.Self != nil) == c.party || p.MPCost != c.mp || p.HPCost != 0 || p.Cooldown != c.cooldown || b.Duration != c.duration || !reflect.DeepEqual(b.Stats, c.stats) {
			t.Errorf("skill %d: plan %+v self %+v party %+v", c.id, p, p.Self, p.Party)
			continue
		}
		for _, s := range b.Stats {
			if _, ok := buffstat.Lookup(s.Stat); !ok {
				t.Errorf("skill %d: unknown buff stat %s", c.id, s.Stat)
			}
		}
	}
}

func TestCastHeals(t *testing.T) {
	for _, id := range []int{2121004, 2221004, 2321004} {
		if p := cast(t, id, 30); p.Heal == nil || *p.Heal != (skills.Heal{HPPercent: 10, MPPercent: 10}) {
			t.Errorf("infinity %d heal %+v", id, p.Heal)
		}
	}
	if p := cast(t, 2311009, 20); p.Heal == nil || *p.Heal != (skills.Heal{HPPercent: 100}) {
		t.Errorf("holy magic shell heal %+v", p.Heal)
	}
}

func TestHerosWillDispelsTheCaster(t *testing.T) {
	for _, id := range []int{2121008, 2221008, 2321009} {
		if p := cast(t, id, 5); !p.Dispel || p.Self != nil || p.Party != nil || p.MPCost != 30 || p.Cooldown != 360*time.Second {
			t.Errorf("hero's will %d: %+v", id, p)
		}
	}
}

func TestPartyHealAndDispel(t *testing.T) {
	defer func(r func(int) bool) { roll = r }(roll)
	def, v := level(t, 2301002, 20)
	ch := domain.Character{INT: 400, Equipment: []domain.Equipment{{ItemID: 1382000, Slot: -11, MATK: 100}}}
	for i := 0; i < 20; i++ {
		p, err := healCast(&skills.CastContext{Skill: def, Level: 20, Values: v, Caster: ch, Now: time.Unix(0, 0)})
		if err != nil || p.Party == nil || len(p.Party.Stats) != 0 || p.Area == nil || p.Heal == nil || p.MPCost != 24 {
			t.Fatalf("heal plan %+v %v", p, err)
		}
		if p.Heal.HP < 900 || p.Heal.HP > 1500 { // 3..5 × hp 300% × MATK 100
			t.Fatalf("heal %d outside Java makeHealHP", p.Heal.HP)
		}
	}
	for _, ok := range []bool{true, false} {
		roll = func(p int) bool { return p == 100 && ok }
		p := cast(t, 2311001, 10)
		if p.Party == nil || len(p.Party.Stats) != 0 || p.Area == nil || p.Dispel != ok || p.MPCost != 10 {
			t.Errorf("dispel roll %v: %+v", ok, p)
		}
	}
}

func TestResurrectionRevivesTheParty(t *testing.T) {
	p := cast(t, 2321006, 5)
	if !p.Revive || p.Party == nil || len(p.Party.Stats) != 0 || p.Area == nil || p.MPCost != 150 || p.Cooldown != 600*time.Second {
		t.Fatalf("resurrection %+v", p)
	}
}

func TestItemCosts(t *testing.T) {
	if p := cast(t, 2311002, 10); !reflect.DeepEqual(p.Items, []skills.ItemCost{{ItemID: 4006000, Quantity: 1}}) || p.Self == nil || p.Self.Duration != 200*time.Second || p.Self.Stats[0] != (skills.StatValue{Stat: "SOULARROW"}) {
		t.Errorf("mystic door %+v %+v", p, p.Self)
	}
	if p := cast(t, 2311005, 10); p.Items != nil || p.Cooldown != 10*time.Second || p.MPCost != 35 {
		t.Errorf("doom %+v", p)
	}
}

func TestTeleportOnlySpendsMP(t *testing.T) {
	for _, id := range []int{2101002, 2201002, 2301001} {
		if p := cast(t, id, 20); !reflect.DeepEqual(p, skills.CastPlan{MPCost: 12}) {
			t.Errorf("teleport %d: %+v", id, p)
		}
	}
}

func TestPassiveBonuses(t *testing.T) {
	bonus := func(id, lv, weapon int) skills.Bonus {
		h, ok := skills.Lookup(id)
		if !ok || h.Kind != skills.KindPassive || h.Passive == nil {
			t.Fatalf("skill %d is not a passive with a bonus", id)
		}
		def, v := level(t, id, lv)
		c := domain.Character{Equipment: []domain.Equipment{{ItemID: weapon, Slot: -11}}}
		return h.Passive(&skills.PassiveContext{Skill: def, Level: lv, Values: v, Character: c})
	}
	for _, id := range []int{2100006, 2200006, 2300006} {
		if b := bonus(id, 20, 1382000); b != (skills.Bonus{Mastery: 50, MATK: 10}) {
			t.Errorf("spell mastery %d with staff: %+v", id, b)
		}
		if b := bonus(id, 20, 1372000); b != (skills.Bonus{Mastery: 50, MATK: 10}) {
			t.Errorf("spell mastery %d with wand: %+v", id, b)
		}
		if b := bonus(id, 20, 1302000); b != (skills.Bonus{}) {
			t.Errorf("spell mastery %d with a sword: %+v", id, b)
		}
	}
	for _, id := range []int{2110009, 2210009, 2310010} {
		if b := bonus(id, 20, 0); b != (skills.Bonus{Critical: 30, CriticalDamageMin: 25}) {
			t.Errorf("critical passive %d: %+v", id, b)
		}
	}
	for _, id := range []int{2121009, 2221009, 2321010} {
		if b := bonus(id, 10, 0); b != (skills.Bonus{MATK: 30, BuffDurationPercent: 50}) {
			t.Errorf("buff mastery %d: %+v", id, b)
		}
	}
	if b := bonus(2310008, 20, 0); b != (skills.Bonus{Critical: 40, ACCPercent: 15}) {
		t.Errorf("holy focus: %+v", b)
	}
	if b := bonus(2310008, 20, 1372000); b != (skills.Bonus{Critical: 40, ACCPercent: 15, Mastery: 65}) {
		t.Errorf("holy focus with a wand: %+v", b)
	}
	// The original cleric mastery lookup prefers a learned Holy Focus.
	def, v := level(t, 2300006, 20)
	priest := domain.Character{Job: 231, Level: 200, Equipment: []domain.Equipment{{ItemID: 1372000, Slot: -11}},
		Skills: []domain.SkillState{{ID: 2300006, Level: 20}, {ID: 2310008, Level: 20}}}
	h, _ := skills.Lookup(2300006)
	if b := h.Passive(&skills.PassiveContext{Skill: def, Level: 20, Values: v, Character: priest}); b != (skills.Bonus{MATK: 10}) {
		t.Errorf("spell mastery beside holy focus: %+v", b)
	}
	if b := skills.Bonuses(priest, time.Unix(100, 0)); b.Mastery != 65 {
		t.Errorf("priest mastery: %d", b.Mastery)
	}
	for _, id := range []int{2100007, 2200007, 2300007} {
		if b := bonus(id, 10, 0); b != (skills.Bonus{INT: 40}) {
			t.Errorf("high wisdom %d: %+v", id, b)
		}
	}
	for _, id := range []int{2110001, 2210001} {
		if b := bonus(id, 20, 0); b != (skills.Bonus{MPConsume: 50, ElementAmp: 50}) {
			t.Errorf("element amplification %d: %+v", id, b)
		}
	}
	for _, id := range []int{2120010, 2220010, 2320011} {
		if b := bonus(id, 30, 0); b != (skills.Bonus{DamagePercent: 40, IgnoreDefense: 20}) {
			t.Errorf("arcane aim %d: %+v", id, b)
		}
	}
	if b := bonus(2110000, 10, 0); b != (skills.Bonus{DOTTime: 10, DOTPercent: 10}) {
		t.Errorf("elemental drain F/P: %+v", b)
	}
	if b := bonus(2210000, 10, 0); b != (skills.Bonus{DOTPercent: 10}) {
		t.Errorf("elemental drain I/L: %+v", b)
	}
}

func spec(t *testing.T, id int) *combat.AttackSpec {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Attack == nil {
		t.Fatalf("skill %d has no attack spec", id)
	}
	return h.Attack
}

// prepared runs the spec's Prepare on the level's effect.
func prepared(t *testing.T, id, lv int, ch domain.Character) combat.Effect {
	t.Helper()
	def, _ := level(t, id, lv)
	e, err := combat.FromSkill(def, lv)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &combat.AttackContext{Character: ch, Effect: e, Now: time.Unix(100, 0)}
	if err := spec(t, id).Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx.Effect
}

// The on-hit statuses are Java MapleStatEffect's FREEZE and STUN lists and
// its POISON for a WZ dot, restricted to thread M.
func TestOnHitStatusesFollowJava(t *testing.T) {
	want := map[combat.MobStat][]int{
		combat.MobFreeze: {2121006, 2201004, 2211002, 2211006, 2221001, 2221003, 2221005, 2221007},
		combat.MobStun:   {2121005, 2211003, 2221006, 2311004},
		combat.MobPoison: {2101005, 2111003, 2111006, 2121001, 2121006, 2121007, 2211006, 2221003, 2221007},
	}
	got := map[combat.MobStat][]int{}
	for _, id := range skills.IDs() {
		h, _ := skills.Lookup(id)
		if skills.OwnerOf(id) != "magician" || h.Attack == nil || h.Attack.Prepare == nil {
			continue
		}
		e := prepared(t, id, 10, domain.Character{})
		for _, a := range append(append([]combat.MobStatusApply(nil), e.MonsterStatus...), h.Attack.OnHit...) {
			got[a.Stat] = append(got[a.Stat], id)
			if h.Attack.Kind == combat.Summoned {
				if a.Duration != summonStatusTime && !a.Poison {
					t.Errorf("summon %d: %v lasts %v, Java uses 4 s", id, a.Stat, a.Duration)
				}
			} else if a.Stat == combat.MobFreeze && a.Duration != 2*e.Time {
				t.Errorf("skill %d: freeze %v, want twice %v", id, a.Duration, e.Time)
			}
			if (a.Stat == combat.MobPoison) != a.Poison {
				t.Errorf("skill %d: poison flag on %v", id, a.Stat)
			}
		}
		if e.Element != elements[id] {
			t.Errorf("skill %d: element %v", id, e.Element)
		}
	}
	for _, ids := range got {
		sort.Ints(ids)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("on-hit statuses %v, want %v", got, want)
	}
}

func TestElementalResetMakesAttacksNeutral(t *testing.T) {
	ch := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 2211008, Stat: "ELEMENT_RESET", ExpiresAt: time.Unix(200, 0)}}}
	if e := prepared(t, 2201004, 10, ch); e.Element != combat.Physical {
		t.Fatalf("cold beam under Elemental Reset is %v", e.Element)
	}
	ch.SkillBuffs[0].ExpiresAt = time.Unix(50, 0)
	if e := prepared(t, 2201004, 10, ch); e.Element != combat.Ice {
		t.Fatalf("cold beam after Elemental Reset is %v", e.Element)
	}
}

func TestMPEaterAbsorbsFromNonBosses(t *testing.T) {
	defer func(r func(int) bool) { roll = r }(roll)
	var props []int
	roll = func(p int) bool { props = append(props, p); return true }
	ch := domain.Character{Job: 211, Skills: []domain.SkillState{{ID: 2100000, Level: 20}}}
	ctx := &combat.AttackContext{Character: ch, Now: time.Unix(0, 0), Targets: []combat.TargetInfo{{Mob: content.Mob{MP: 1000}}, {Mob: content.Mob{MP: 5000, Boss: true}}}}
	if _, mp := spec(t, 2101004).Drain(ctx, 10); mp != 100 || !reflect.DeepEqual(props, []int{10}) {
		t.Fatalf("MP Eater 20 gave %d MP, rolls %v", mp, props)
	}
	if _, mp := spec(t, 2101004).Drain(ctx, 0); mp != 0 {
		t.Fatal("MP Eater without damage")
	}
	ctx.Character.Job = 221
	if _, mp := spec(t, 2201004).Drain(ctx, 10); mp != 0 {
		t.Fatal("I/L uses its own MP Eater 2200000")
	}
	if spec(t, 2121005).Drain != nil {
		t.Fatal("summon attacks do not feed MP Eater")
	}
}

func TestCastOnlySpecs(t *testing.T) {
	for id, want := range map[int]combat.MobStatusApply{
		2101003: {Stat: combat.MobSpeed, ValueFrom: "x"}, 2201003: {Stat: combat.MobSpeed, ValueFrom: "x"},
		2111004: {Stat: combat.MobSeal, Value: 1}, 2211004: {Stat: combat.MobSeal, Value: 1},
		2311005: {Stat: combat.MobDoom, Value: 1},
	} {
		s := spec(t, id)
		if s.Kind != 0 || s.MobCast == nil || !s.MobCast.UseBox || !reflect.DeepEqual(s.MobCast.Statuses, []combat.MobStatusApply{want}) {
			t.Errorf("monster cast %d: %+v", id, s)
		}
		if p := cast(t, id, 10); p.Self != nil || p.Party != nil || p.MPCost < 1 {
			t.Errorf("monster cast %d plan %+v", id, p)
		}
	}
	mist := spec(t, 2111003)
	if mist.Kind != combat.Magic || mist.Field == nil || mist.Field.Kind != combat.FieldArea || mist.Field.AreaType != protocol.AreaPoison || mist.Field.Box != (combat.Box{LT: combat.Point{X: -200, Y: -150}, RB: combat.Point{X: 200, Y: 150}, Set: true}) || !reflect.DeepEqual(mist.Field.MobStatus, []combat.MobStatusApply{poison}) {
		t.Errorf("poison mist %+v", mist.Field)
	}
	if !reflect.DeepEqual(spec(t, 2121003).EruptAreas, []int{2111003}) {
		t.Error("mist eruption must detonate poison mists")
	}
	if door := spec(t, 2311002); door.Kind != 0 || door.Field == nil || door.Field.Kind != combat.FieldTownPortal {
		t.Errorf("mystic door %+v", door)
	}
}

func TestSummons(t *testing.T) {
	for _, id := range []int{2121005, 2221005, 2321003} {
		s := spec(t, id)
		if s.Kind != combat.Summoned || s.Summon == nil || s.Summon.MoveAbility != 1 || s.Summon.AssistType != 1 || s.Summon.Multi {
			t.Errorf("summon %d: %+v %+v", id, s, s.Summon)
		}
		p := cast(t, id, 30)
		if p.Self == nil || len(p.Self.Stats) != 1 || p.Self.Stats[0] != (skills.StatValue{Stat: "SUMMON", Value: 1}) || p.Self.Duration < time.Second {
			t.Errorf("summon %d buff %+v", id, p.Self)
		}
	}
}

func TestHealOnlyDamagesUndead(t *testing.T) {
	s := spec(t, 2301002)
	dc := combat.DamageContext{Stats: combat.PlayerStats{INT: 500, MATK: 100}, Effect: combat.Effect{SkillID: 2301002, Damage: 300}, Magic: true}
	if s.MaxDamage(dc) != 0 {
		t.Fatal("heal damaged a living monster")
	}
	ctx := &combat.AttackContext{Effect: combat.Effect{MPCon: 24}}
	if err := s.Prepare(ctx); err != nil || ctx.Effect.MPCon != 0 || ctx.Effect.Element != combat.Holy {
		t.Fatalf("heal attack must be free after the paid cast: %+v %v", ctx.Effect, err)
	}
	dc.Mob.Undead = true
	if got := s.MaxDamage(dc); got < 1 || got != combat.LineCeiling(dc) {
		t.Fatalf("heal on undead ceiling %d", got)
	}
}
