package warrior

import (
	"encoding/json"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

// The original warrior inventory remains registered alongside the v148 jobs.
func TestRegistersEveryInventorySkill(t *testing.T) {
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
	for _, s := range inv.Skills {
		if s.Thread == "W" {
			want = append(want, s.ID)
		}
	}
	for _, id := range skills.IDs() {
		if h, _ := skills.Lookup(id); h.Package() == "warrior" && !domain.IsMihileJob(id/10000) {
			got = append(got, id)
		}
	}
	sort.Ints(want)
	if len(want) != 85 || !reflect.DeepEqual(got, want) {
		t.Fatalf("registered %d %v\ninventory %d %v", len(got), got, len(want), want)
	}
}

func castCtx(t *testing.T, id, level int) *skills.CastContext {
	t.Helper()
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	values, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return &skills.CastContext{Skill: def, Level: level, Values: values, Now: time.Unix(0, 0)}
}

func cast(t *testing.T, id, level int) (skills.CastPlan, error) {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	return h.Cast(castCtx(t, id, level))
}

// Buff values follow Java MapleStatEffect statups for the WZ level values.
func TestBuffPlans(t *testing.T) {
	cases := []struct {
		id, level, mp int
		secs          int
		party         bool
		stats         []skills.StatValue
	}{
		{1001003, 20, 14, 300, false, []skills.StatValue{stat("WDEF", 200)}},
		{1101004, 1, 30, 10, false, []skills.StatValue{stat("BOOSTER", -2)}},
		{1101004, 20, 11, 200, false, []skills.StatValue{stat("BOOSTER", -2)}},
		{1201004, 20, 11, 200, false, []skills.StatValue{stat("BOOSTER", -2)}},
		{1301004, 20, 11, 200, false, []skills.StatValue{stat("BOOSTER", -2)}},
		{1101006, 20, 20, 155, true, []skills.StatValue{stat("WATK", 30)}},
		{1101007, 20, 20, 180, false, []skills.StatValue{stat("POWERGUARD", 30)}},
		{1201007, 20, 20, 180, false, []skills.StatValue{stat("POWERGUARD", 50)}},
		{1111002, 1, 22, 100, false, []skills.StatValue{stat("COMBO", 1)}},
		{1111002, 20, 34, 200, false, []skills.StatValue{stat("COMBO", 1)}},
		{1121000, 30, 70, 900, true, []skills.StatValue{stat("MAPLE_WARRIOR", 15)}},
		{1221000, 1, 10, 30, true, []skills.StatValue{stat("MAPLE_WARRIOR", 1)}},
		{1321000, 30, 70, 900, true, []skills.StatValue{stat("MAPLE_WARRIOR", 15)}},
		{1121002, 30, 60, 300, false, []skills.StatValue{stat("STANCE", 95)}},
		{1221002, 30, 60, 300, false, []skills.StatValue{stat("STANCE", 95)}},
		{1321002, 1, 30, 10, false, []skills.StatValue{stat("STANCE", 37)}},
		{1211004, 20, 35, 200, false, []skills.StatValue{stat("WK_CHARGE", 1)}},
		{1211006, 20, 35, 200, false, []skills.StatValue{stat("WK_CHARGE", 1)}},
		{1211008, 20, 35, 200, false, []skills.StatValue{stat("WK_CHARGE", 1)}},
		{1221004, 20, 35, 300, false, []skills.StatValue{stat("WK_CHARGE", 1)}},
		{1211011, 1, 24, 66, true, []skills.StatValue{stat("COMBAT_ORDERS", 1)}},
		{1211011, 20, 36, 180, true, []skills.StatValue{stat("COMBAT_ORDERS", 2)}},
		{1301006, 20, 24, 240, true, []skills.StatValue{stat("WDEF", 300), stat("MDEF", 200)}},
		{1301007, 20, 60, 300, true, []skills.StatValue{stat("MAXHP", 60), stat("MAXMP", 60)}},
		{1301007, 1, 20, 15, true, []skills.StatValue{stat("MAXHP", 3), stat("MAXMP", 3)}},
		{1311008, 20, 24, 200, false, []skills.StatValue{stat("STR", 60), stat("DRAGONBLOOD", 0)}},
	}
	for _, c := range cases {
		p, err := cast(t, c.id, c.level)
		if err != nil {
			t.Errorf("%d/%d: %v", c.id, c.level, err)
			continue
		}
		b := p.Self
		if c.party {
			if p.Self != nil {
				t.Errorf("%d: party buff also has a self part", c.id)
			}
			b = p.Party
			if p.Area == nil || p.Area.Left != -p.Area.Right || p.Area.Left >= 0 || p.Area.Top >= p.Area.Bottom {
				t.Errorf("%d: party area %+v", c.id, p.Area)
			}
		} else if p.Party != nil {
			t.Errorf("%d: self buff has a party part", c.id)
		}
		if b == nil || p.MPCost != c.mp || b.Duration != time.Duration(c.secs)*time.Second || !reflect.DeepEqual(b.Stats, c.stats) {
			t.Errorf("%d/%d: plan %+v buff %+v", c.id, c.level, p, b)
			continue
		}
		names := make([]buffstat.Stat, len(b.Stats))
		for i, s := range b.Stats {
			names[i] = s.Stat
		}
		if _, err := buffstat.Resolve(names); err != nil {
			t.Errorf("%d: %v", c.id, err)
		}
	}
}

// Buffs the core cannot apply yet keep their Java numbers for when it can.
func TestPendingBuffValues(t *testing.T) {
	c := castCtx(t, 1121010, 30)
	c.Now = time.Unix(100, 0)
	if _, err := enrage(c); err == nil {
		t.Fatal("enrage without orbs accepted")
	}
	c.Caster = comboBuff(10)
	if _, err := enrage(c); err == nil {
		t.Fatal("enrage with 9 orbs accepted")
	}
	c.Caster = comboBuff(11)
	p, err := enrage(c)
	want := []skills.StatValue{{Stat: "ENRAGE", Value: 4001}, {Stat: "SHARP_EYES", Value: 5200}}
	if err != nil || p.MPCost != 50 || p.Self.Duration != 240*time.Second || !reflect.DeepEqual(p.Self.Stats, want) || !reflect.DeepEqual(p.Consume, []skills.StatValue{stat("COMBO", 10)}) {
		t.Fatalf("enrage %+v %v", p, err)
	}
	p, err = divineShield(castCtx(t, 1220013, 10))
	want = []skills.StatValue{{Stat: "DIVINE_SHIELD", Value: 11}, {Stat: "ENHANCED_WATK", Value: 30}}
	if err != nil || p.Cooldown != 30*time.Second || p.Self.Duration != 90*time.Second || !reflect.DeepEqual(p.Self.Stats, want) {
		t.Fatalf("divine shield %+v %v", p, err)
	}
}

func TestCostOnlyCasts(t *testing.T) {
	for _, c := range []struct{ id, level, mp int }{{1101010, 10, 15}, {1201010, 10, 15}, {1301010, 10, 15}, {1111007, 20, 15}, {1211009, 1, 35}, {1311007, 20, 15}, {1201006, 20, 24}} {
		p, err := cast(t, c.id, c.level)
		if err != nil || p.MPCost != c.mp || p.Self != nil || p.Party != nil {
			t.Errorf("%d: %+v %v", c.id, p, err)
		}
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

// Adventurer attacks use the melee packet. Summons and monster casts use their
// own packets.
func TestAttacksUseTheirPacket(t *testing.T) {
	n := 0
	for _, id := range skills.IDs() {
		h, _ := skills.Lookup(id)
		if h.Package() != "warrior" || h.Attack == nil || domain.IsMihileJob(id/10000) {
			continue
		}
		if h.Attack.MobCast != nil || h.Attack.Summon != nil {
			if h.Attack.Kind != 0 || h.Kind&skills.KindAttack != 0 {
				t.Errorf("%d: non-attack spec %+v kind %b", id, h.Attack, h.Kind)
			}
			continue
		}
		n++
		if h.Attack.Kind != combat.Melee || h.Kind&skills.KindAttack == 0 {
			t.Errorf("%d: %+v kind %b", id, h.Attack, h.Kind)
		}
	}
	if n != 27 {
		t.Fatalf("%d warrior attacks", n)
	}
}

func TestMihileSoulBladeAndSwordMastery(t *testing.T) {
	if got := spec(t, 51001004); got.Kind != combat.Shoot || got.Ammo != combat.AmmoNone {
		t.Fatalf("Soul Blade spec = %+v", got)
	}
	for _, tc := range []struct {
		item int
		want skills.Bonus
	}{
		{1302000, skills.Bonus{Mastery: 50, ACC: 120}},
		{1402000, skills.Bonus{}},
	} {
		character := wielding(tc.item)
		character.Job = 5110
		c := skills.PassiveContext{Character: character, Values: map[string]int{"mastery": 50, "accX": 120}}
		if got := mihileSwordMastery(&c); got != tc.want {
			t.Errorf("weapon %d gives %+v, want %+v", tc.item, got, tc.want)
		}
	}
	advanced := wielding(1302000)
	advanced.Job = 5112
	advanced.Skills = append(advanced.Skills, domain.SkillState{ID: 51120001, Level: 30})
	c := skills.PassiveContext{Character: advanced, Values: map[string]int{"mastery": 50, "accX": 120}}
	if got := mihileSwordMastery(&c); got.Mastery != 0 || got.ACC != 120 {
		t.Errorf("Advanced Sword Mastery did not replace the basic mastery: %+v", got)
	}
}

func TestMihileV148AttackFamilies(t *testing.T) {
	for _, id := range []int{51100002, 51101005, 51101006, 51111006, 51120002, 51121007} {
		if got := spec(t, id).Kind; got != combat.Melee {
			t.Errorf("%d is family %v, want 0x55", id, got)
		}
	}
	for _, id := range []int{51001004, 51111007, 51121008} {
		got := spec(t, id)
		if got.Kind != combat.Shoot || got.Ammo != combat.AmmoNone {
			t.Errorf("%d is %+v, want ammo-free 0x56", id, got)
		}
	}
	for _, id := range []int{51111007, 51121008} {
		if got := spec(t, id).OnHit; !reflect.DeepEqual(got, stun) {
			t.Errorf("%d stun effect = %+v", id, got)
		}
	}
	darkness := spec(t, 51121007).OnHit
	if len(darkness) != 1 || darkness[0].Stat != combat.MobDarkness || darkness[0].ValueFrom != "x" {
		t.Fatalf("Soul Assault darkness = %+v", darkness)
	}
	clock := time.Unix(100, 0)
	effect := combat.Effect{SkillID: 51121007, X: 15, Prop: 25, Time: 70 * time.Second}
	for _, target := range []struct {
		boss bool
		want time.Duration
	}{
		{false, 70 * time.Second},
		{true, 35 * time.Second},
	} {
		mob := content.Mob{Boss: target.boss}
		status, ok := combat.ResolveOn(darkness[0], effect, 1, 100, mob, clock, func(prop int) bool { return prop == 25 })
		if !combat.CanInflict(mob, darkness[0].Stat, combat.Physical) || !ok || status.Value != 15 || status.ExpiresAt.Sub(clock) != target.want {
			t.Errorf("Soul Assault darkness on boss=%v = %+v", target.boss, status)
		}
	}
	for _, id := range []int{51100002, 51120002} {
		prepare := spec(t, id).Prepare
		if prepare(&combat.AttackContext{Character: wielding(1302000)}) != nil || prepare(&combat.AttackContext{Character: wielding(1402000)}) == nil {
			t.Errorf("%d must require a one-handed sword", id)
		}
	}
}

// Java MapleStatEffect monsterStatus entries for the attacks that inflict one.
func TestAttackStatuses(t *testing.T) {
	stun := []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1}}
	for _, id := range []int{1111005, 1111008, 1121001, 1321001, 1211002} {
		if got := spec(t, id).OnHit; !reflect.DeepEqual(got, stun) {
			t.Errorf("%d: %+v", id, got)
		}
	}
	if got := spec(t, 1111003).OnHit; !reflect.DeepEqual(got, []combat.MobStatusApply{{Stat: combat.MobDarkness, ValueFrom: "x"}}) {
		t.Errorf("panic: %+v", got)
	}
	for _, id := range []int{1121006, 1121008, 1221011, 1311003, 1321012} {
		if got := spec(t, id).OnHit; len(got) != 0 {
			t.Errorf("%d: %+v", id, got)
		}
	}
}

func prepared(t *testing.T, id int, ch domain.Character, e combat.Effect) (combat.Effect, error) {
	t.Helper()
	s := spec(t, id)
	if s.Prepare == nil {
		return e, nil
	}
	ctx := &combat.AttackContext{Kind: combat.Melee, Character: ch, Effect: e, Now: time.Unix(100, 0)}
	err := s.Prepare(ctx)
	return ctx.Effect, err
}

func buffed(ids ...int) domain.Character {
	var ch domain.Character
	for _, id := range ids {
		ch.SkillBuffs = append(ch.SkillBuffs, domain.SkillBuff{ID: id, ExpiresAt: time.Unix(200, 0)})
	}
	ch.SkillBuffs = append(ch.SkillBuffs, domain.SkillBuff{ID: 1211004, ExpiresAt: time.Unix(50, 0)}) // expired
	return ch
}

func comboBuff(value int) domain.Character {
	return domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 1111002, Stat: "COMBO", Value: value, ExpiresAt: time.Unix(200, 0)}}}
}

// v148 WZ y sets the required orb count for each finisher.
func TestFinishersNeedCombo(t *testing.T) {
	for _, tc := range []struct{ id, required int }{{1111003, 2}, {1111005, 1}} {
		cast := castCtx(t, tc.id, 20)
		eff, err := combat.FromSkill(cast.Skill, 20)
		if err != nil || eff.Y != tc.required {
			t.Fatalf("%d expected WZ cost %d: %+v %v", tc.id, tc.required, eff, err)
		}
		if _, err := prepared(t, tc.id, buffed(), eff); err == nil {
			t.Errorf("%d without combo accepted", tc.id)
		}
		if _, err := prepared(t, tc.id, comboBuff(tc.required), eff); err == nil {
			t.Errorf("%d accepted with one fewer orb", tc.id)
		}
		e, err := prepared(t, tc.id, comboBuff(tc.required+1), eff)
		if err != nil || e.Damage != eff.Damage*tc.required {
			t.Errorf("%d with %d orbs: %d %v", tc.id, tc.required, e.Damage, err)
		}
	}
}

func trigger(t *testing.T, id, level int, ch domain.Character, attack int, damage int) *skills.BuffChange {
	t.Helper()
	h, _ := skills.Lookup(id)
	c := castCtx(t, id, level)
	ctx := &skills.TriggerContext{Skill: c.Skill, Level: level, Values: c.Values, Character: ch, Damage: damage, Now: time.Unix(100, 0)}
	for _, b := range ch.SkillBuffs {
		if b.ID == id {
			ctx.Rows = append(ctx.Rows, b)
		}
	}
	if attack != 0 {
		ctx.Attack = &combat.AttackEvent{SkillID: attack}
		return h.OnAttack(ctx)
	}
	return h.OnDamaged(ctx)
}

func comboAfter(t *testing.T, ch domain.Character, attack int) int {
	t.Helper()
	ch.Skills = append(ch.Skills, domain.SkillState{ID: 1111002, Level: 20})
	if attack == 1111003 || attack == 1111005 {
		ch.Skills = append(ch.Skills, domain.SkillState{ID: attack, Level: 20})
	}
	got := trigger(t, 1111002, 20, ch, attack, 0)
	if got == nil {
		v, _ := skills.ActiveStat(ch, "COMBO", time.Unix(100, 0))
		return v
	}
	if got.Cancel || got.Buff.Duration != 100*time.Second || len(got.Buff.Stats) != 1 || got.Buff.Stats[0].Stat != "COMBO" {
		t.Fatalf("combo change %+v", got)
	}
	return got.Buff.Stats[0].Value
}

// v148 finisher cost plus normal orb gain.
func TestComboOrbs(t *testing.T) {
	cases := []struct{ value, attack, want int }{
		{1, 1001004, 2}, {5, 1121008, 6}, {6, 1121008, 6}, // level 20 x = 5 orbs
		{4, 1111008, 4}, {4, 1111002, 4}, // Shout and the cast give none
		{4, 1111003, 2}, {4, 1111005, 3}, {3, 1111003, 1}, {2, 1111005, 1},
	}
	for _, c := range cases {
		if got := comboAfter(t, comboBuff(c.value), c.attack); got != c.want {
			t.Errorf("COMBO %d after %d: %d want %d", c.value, c.attack, got, c.want)
		}
	}
	if got := trigger(t, 1111002, 20, domain.Character{}, 1001004, 0); got != nil {
		t.Errorf("no combo buff: %+v", got)
	}
	adv := comboBuff(6)
	adv.Skills = []domain.SkillState{{ID: 1120003, Level: 30}} // x = 10 orbs
	if got := comboAfter(t, adv, 1121008); got != 7 && got != 8 {
		t.Errorf("advanced combo %d", got)
	}
	adv = comboBuff(11)
	adv.Skills = []domain.SkillState{{ID: 1120003, Level: 30}}
	if got := comboAfter(t, adv, 1121008); got != 11 {
		t.Errorf("advanced combo cap %d", got)
	}
}

// Java damage handling for 1220013: blocks count down, then the shield ends.
func TestDivineShieldHits(t *testing.T) {
	live := func(blocks int) domain.Character {
		return domain.Character{SkillBuffs: []domain.SkillBuff{
			{ID: 1220013, Stat: "DIVINE_SHIELD", Value: blocks, ExpiresAt: time.Unix(190, 0)},
			{ID: 1220013, Stat: "ENHANCED_WATK", Value: 30, ExpiresAt: time.Unix(190, 0)},
		}}
	}
	got := trigger(t, 1220013, 10, live(11), 0, 50)
	if got == nil || got.Cancel || got.Buff.Duration != 90*time.Second || !reflect.DeepEqual(got.Buff.Stats, []skills.StatValue{stat("DIVINE_SHIELD", 10), stat("ENHANCED_WATK", 30)}) {
		t.Fatalf("block %+v", got)
	}
	if got = trigger(t, 1220013, 10, live(1), 0, 50); got == nil || !got.Cancel || got.Cooldown != 10*time.Second {
		t.Fatalf("last block %+v", got)
	}
	if got = trigger(t, 1220013, 10, live(5), 0, 0); got != nil {
		t.Fatalf("miss %+v", got)
	}
	procs := 0
	for range 400 {
		if got = trigger(t, 1220013, 10, domain.Character{}, 0, 50); got != nil {
			procs++
			if got.Cancel || !reflect.DeepEqual(got.Buff.Stats, []skills.StatValue{stat("DIVINE_SHIELD", 11), stat("ENHANCED_WATK", 30)}) {
				t.Fatalf("proc %+v", got.Buff)
			}
		}
	}
	if procs < 60 || procs > 190 { // prop 30%
		t.Errorf("%d procs in 400 hits", procs)
	}
	cooling := domain.Character{SkillCooldowns: []domain.SkillCooldown{{ID: 1220013, ExpiresAt: time.Unix(105, 0)}}}
	for range 100 {
		if got = trigger(t, 1220013, 10, cooling, 0, 50); got != nil {
			t.Fatalf("proc while cooling %+v", got)
		}
	}
}

// Java DamageParse: the active WK_CHARGE source is the attack element.
func TestChargeElement(t *testing.T) {
	cases := []struct {
		charge int
		want   combat.Element
	}{{0, combat.Physical}, {1211004, combat.Fire}, {1211006, combat.Ice}, {1211008, combat.Lightning}, {1221004, combat.Holy}}
	for _, id := range []int{1200002, 1201008, 1211002, 1221007, 1221009} {
		for _, c := range cases {
			ch := buffed()
			if c.charge != 0 {
				ch = buffed(c.charge)
			}
			e, err := prepared(t, id, ch, combat.Effect{})
			if err != nil || e.Element != c.want {
				t.Errorf("%d with %d: %v %v", id, c.charge, e.Element, err)
			}
		}
	}
	if e, _ := prepared(t, 1221011, buffed(1211004), combat.Effect{}); e.Element != combat.Holy {
		t.Errorf("heaven's hammer %v", e.Element)
	}
}

func TestHPRatioAttacks(t *testing.T) {
	e, err := prepared(t, 1311005, domain.Character{}, combat.Effect{X: 5})
	if err != nil || e.HPRatio != 5 || e.IgnoreMobDefense != 100 {
		t.Errorf("sacrifice %+v %v", e, err)
	}
	e, err = prepared(t, 1311006, domain.Character{}, combat.Effect{X: 10})
	if err != nil || e.HPRatio != 10 || e.IgnoreMobDefense != 0 {
		t.Errorf("dragon roar %+v %v", e, err)
	}
}

// Java MapleCharacter.onAttack: prop% chance, x% of damage, at most maxHP/2.
func TestDragonJudgementDrain(t *testing.T) {
	if prop, x := dragonJudgementValues(nil); prop != 0 || x != 0 {
		t.Fatal("drain without the skill")
	}
	if prop, x := dragonJudgementValues([]domain.SkillState{{ID: 1310009, Level: 10}}); prop != 20 || x != 10 {
		t.Fatalf("level 10 prop %d x %d", prop, x)
	}
	if got := drained(5000, 10, 2000); got != 500 {
		t.Errorf("drained %d", got)
	}
	if got := drained(50000, 10, 2000); got != 1000 {
		t.Errorf("capped %d", got)
	}
	ctx := &combat.AttackContext{Character: domain.Character{MaxHP: 2000}}
	if hp, mp := dragonJudgementDrain(ctx, 5000); hp != 0 || mp != 0 {
		t.Errorf("unlearned drain %d %d", hp, mp)
	}
	for _, id := range []int{1300002, 1301008, 1311001, 1311003, 1311005, 1311006, 1321001, 1321003, 1321012} {
		if spec(t, id).Drain == nil {
			t.Errorf("%d has no drain", id)
		}
	}
	if spec(t, 1121008).Drain != nil {
		t.Error("hero attack drains")
	}
}

func TestMagicCrashAndBeholder(t *testing.T) {
	for _, id := range []int{1111007, 1211009, 1311007} {
		m := spec(t, id).MobCast
		if m == nil || !m.Dispel || !m.UseBox || !reflect.DeepEqual(m.Statuses, []combat.MobStatusApply{{Stat: combat.MobMagicCrash, Value: 1}}) {
			t.Errorf("%d: %+v", id, m)
		}
	}
	m := spec(t, 1201006).MobCast
	want := []combat.MobStatusApply{{Stat: combat.MobPAD, ValueFrom: "x", SubTime: true}, {Stat: combat.MobPDR, ValueFrom: "x", SubTime: true}, {Stat: combat.MobDarkness, ValueFrom: "z", SubTime: true}}
	if m == nil || m.Dispel || !m.UseBox || !reflect.DeepEqual(m.Statuses, want) {
		t.Errorf("threaten %+v", m)
	}
	s := spec(t, 1321007).Summon
	if s == nil || s.MoveAbility != 1 || s.AssistType != 2 || s.Multi || s.Puppet || s.Sub == nil {
		t.Fatalf("beholder %+v", s)
	}
	p, err := cast(t, 1321007, 10)
	if err != nil || p.MPCost != 60 || p.Self == nil || p.Self.Duration != 1200*time.Second || !reflect.DeepEqual(p.Self.Stats, []skills.StatValue{stat("BEHOLDER", 10)}) {
		t.Fatalf("beholder cast %+v %v", p, err)
	}
}

func passive(t *testing.T, id, level int, ch domain.Character) skills.Bonus {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Passive == nil {
		t.Fatalf("skill %d has no passive", id)
	}
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	values, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return h.Passive(&skills.PassiveContext{Skill: def, Level: level, Values: values, Character: ch})
}

func wielding(itemID int) domain.Character {
	return domain.Character{Equipment: []domain.Equipment{{Slot: -5, ItemID: 1040002}, {Slot: -11, ItemID: itemID}}}
}

func TestV148PaladinChargeUpgradeUsesLearnedLevel(t *testing.T) {
	t.Setenv("MAPLEGO_WZ_ROOT", t.TempDir())
	handler, ok := skills.Lookup(1211002)
	if !ok || handler.Attack == nil || handler.Attack.LearnedEffect == nil {
		t.Fatal("Charged Blow has no learned effect")
	}
	base := combat.Effect{SkillID: 1211002, Damage: 550, MobCount: 4, AttackCount: 1}
	upgrade := domain.SkillState{ID: 1220010, Level: 10}
	for _, test := range []struct {
		name             string
		job              int
		learned          []domain.SkillState
		damage, mob, hit int
	}{
		{"white knight", 121, []domain.SkillState{upgrade}, 550, 4, 1},
		{"paladin before learning Elemental Force", 122, nil, 550, 4, 1},
		{"paladin with max Elemental Force", 122, []domain.SkillState{upgrade}, 370, 6, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			character := domain.Character{Job: test.job, Skills: test.learned}
			e := base
			if err := handler.Attack.ApplyLearnedEffect(character, time.Time{}, &e); err != nil {
				t.Fatal(err)
			}
			if e.Damage != test.damage || e.MobCount != test.mob || e.AttackCount != test.hit {
				t.Fatalf("Charged Blow effect %+v", e)
			}
		})
	}
}

func TestPassives(t *testing.T) {
	cases := []struct {
		id, level int
		ch        domain.Character
		want      skills.Bonus
	}{
		{1000006, 10, domain.Character{}, skills.Bonus{MaxHPPercent: 20}},
		{1100000, 20, wielding(1302000), skills.Bonus{Mastery: 50, ACC: 120}},
		{1100000, 20, wielding(1412000), skills.Bonus{Mastery: 50, ACC: 120}},
		{1100000, 20, wielding(1432000), skills.Bonus{}},
		{1100000, 20, domain.Character{}, skills.Bonus{}},
		{1200000, 1, wielding(1422000), skills.Bonus{Mastery: 12, ACC: 6}},
		{1300000, 20, wielding(1442000), skills.Bonus{Mastery: 50, ACC: 120}},
		{1300000, 20, wielding(1302000), skills.Bonus{}},
		{1120013, 30, domain.Character{}, skills.Bonus{WATK: 30}},
		{1220010, 10, domain.Character{}, skills.Bonus{Mastery: 70}},
		{1310009, 10, domain.Character{}, skills.Bonus{Critical: 30, CriticalDamageMin: 15}},
		{1100009, 10, domain.Character{}, skills.Bonus{STR: 30, DEX: 30}},
		{1200009, 1, domain.Character{}, skills.Bonus{STR: 3, DEX: 3}},
		{1300009, 10, domain.Character{}, skills.Bonus{STR: 30, DEX: 30}},
		{1110000, 20, domain.Character{}, skills.Bonus{HPRecovery: 100, MPRecovery: 60}},
		{1110009, 20, domain.Character{}, skills.Bonus{DamagePercent: 25}},
		{1120012, 10, domain.Character{}, skills.Bonus{IgnoreDefense: 40}},
		{1210001, 20, domain.Character{}, skills.Bonus{WDEFPercent: 200, MDEFPercent: 200}},
		{1220005, 30, domain.Character{}, skills.Bonus{WDEFPercent: 20}},
		{1220006, 30, domain.Character{}, skills.Bonus{ASR: 30, TER: 30}},
		{1310000, 20, domain.Character{}, skills.Bonus{TER: 50}},
		{1320006, 30, domain.Character{HP: 199, MaxHP: 1000}, skills.Bonus{}},
		{1320006, 30, domain.Character{HP: 200, MaxHP: 1000}, skills.Bonus{DamagePercent: 60, Speed: 20, Critical: 10, CriticalDamageMin: 15}},
	}
	for _, c := range cases {
		if got := passive(t, c.id, c.level, c.ch); got != c.want {
			t.Errorf("%d/%d: %+v want %+v", c.id, c.level, got, c.want)
		}
	}
}

// Java SummonHandler 1321007: Aura heals hp at most every x seconds.
func TestBeholderSubSkills(t *testing.T) {
	values := func(id, level int) map[string]int {
		v, err := castCtx(t, id, level).Skill.Values(level)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	now := time.Unix(1000, 0)
	aura := combat.SubSummonContext{SummonSkill: 1321007, SubSkill: 1320008, SubLevel: 20, SubValues: values(1320008, 20), Now: now}
	r, err := beholderSub(aura)
	if err != nil || r.HealHP != 500 || r.Action != 5 {
		t.Fatalf("aura %+v %v", r, err)
	}
	aura.LastSub = now.Add(-3 * time.Second) // level 20 interval is 4s
	if _, err := beholderSub(aura); err == nil {
		t.Error("aura inside its interval accepted")
	}
	aura.LastSub = now.Add(-4 * time.Second)
	if _, err := beholderSub(aura); err != nil {
		t.Errorf("aura after its interval: %v", err)
	}
	hex := combat.SubSummonContext{SummonSkill: 1321007, SubSkill: 1320009, SubLevel: 20, SubValues: values(1320009, 20), Now: now}
	r, err = beholderSub(hex)
	if err != nil || r.HealHP != 0 || r.Action < 6 || r.Action > 8 || r.Apply == nil {
		t.Fatalf("hex %+v %v", r, err)
	}
	want := &skills.Buff{Duration: 100 * time.Second, Stats: []skills.StatValue{stat("ENHANCED_WATK", 35), stat("ENHANCED_WDEF", 500), stat("ENHANCED_MDEF", 500), stat("ACC", 300), stat("AVOID", 300)}}
	if got := hexBuff(hex.SubValues); !reflect.DeepEqual(got, want) {
		t.Fatalf("hex buff %+v", got)
	}
	names := make([]buffstat.Stat, len(want.Stats))
	for i, s := range want.Stats {
		names[i] = s.Stat
	}
	if _, err := buffstat.Resolve(names); err != nil {
		t.Fatalf("hex stats: %v", err)
	}
	if _, err := beholderSub(combat.SubSummonContext{SubSkill: 1321000, Now: now}); err == nil {
		t.Error("foreign sub-skill accepted")
	}
}

// F2c templates: Hero's Will dispels, Restoration heals x% max HP.
func TestHerosWillAndRestoration(t *testing.T) {
	for _, id := range []int{1121011, 1221012, 1321010} {
		p, err := cast(t, id, 5)
		if err != nil || !p.Dispel || p.MPCost != 30 || p.Cooldown != 360*time.Second || p.Self != nil || p.Party != nil {
			t.Errorf("%d: %+v %v", id, p, err)
		}
	}
	p, err := cast(t, 1211010, 10)
	if err != nil || p.Heal == nil || *p.Heal != (skills.Heal{HPPercent: 40}) || p.MPCost != 40 || p.Cooldown != 15*time.Second {
		t.Fatalf("restoration %+v %v", p, err)
	}
}

// Java applyBuffEffect 1211008: over another charge it adds LIGHTNING_CHARGE.
func TestLightningChargeOverAnotherCharge(t *testing.T) {
	c := castCtx(t, 1211008, 20)
	c.Now = time.Unix(100, 0)
	c.Caster.SkillBuffs = []domain.SkillBuff{{ID: 1211004, Stat: "WK_CHARGE", Value: 1, ExpiresAt: time.Unix(200, 0)}}
	p, err := charge(c)
	if err != nil || !reflect.DeepEqual(p.Self.Stats, []skills.StatValue{stat("LIGHTNING_CHARGE", 1)}) {
		t.Fatalf("over flame %+v %v", p.Self, err)
	}
	c.Caster.SkillBuffs = []domain.SkillBuff{{ID: 1211008, Stat: "WK_CHARGE", Value: 1, ExpiresAt: time.Unix(200, 0)}, {ID: 1211006, Stat: "WK_CHARGE", ExpiresAt: time.Unix(50, 0)}}
	if p, err = charge(c); err != nil || !reflect.DeepEqual(p.Self.Stats, []skills.StatValue{stat("WK_CHARGE", 1)}) {
		t.Fatalf("recast %+v %v", p.Self, err)
	}
}

// Java MapleCharacter damage handling for 1320011 (prop 100).
func TestBeholderRevenge(t *testing.T) {
	dk := domain.Character{Job: 132, STR: 400, DEX: 60, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1432000, WATK: 100}},
		SkillBuffs: []domain.SkillBuff{{ID: 1321007, Stat: "BEHOLDER", Value: 10, ExpiresAt: time.Unix(500, 0)}}}
	c := castCtx(t, 1320011, 10)
	ctx := &skills.TriggerContext{Skill: c.Skill, Level: 10, Values: c.Values, Character: dk, Damage: 100, Mob: 77, Now: time.Unix(100, 0)}
	got := beholderRevenge(ctx)
	stats, _ := combat.AttackStats(dk, ctx.Now)
	want := int(combat.MaxBaseDamage(stats) * 750 / 100)
	if got == nil || got.Cooldown != 5*time.Second || len(got.MobHits) != 1 || got.MobHits[0] != (combat.ServerHit{ObjectID: 77, Damage: want}) || want <= 0 {
		t.Fatalf("revenge %+v want %d", got, want)
	}
	for name, mutate := range map[string]func(*skills.TriggerContext){
		"no beholder": func(x *skills.TriggerContext) { x.Character.SkillBuffs = nil },
		"cooling": func(x *skills.TriggerContext) {
			x.Character.SkillCooldowns = []domain.SkillCooldown{{ID: 1320011, ExpiresAt: time.Unix(101, 0)}}
		},
		"no damage":  func(x *skills.TriggerContext) { x.Damage = 0 },
		"no monster": func(x *skills.TriggerContext) { x.Mob = 0 },
		"not 132":    func(x *skills.TriggerContext) { x.Character.Job = 131 },
	} {
		x := *ctx
		mutate(&x)
		if got := beholderRevenge(&x); got != nil {
			t.Errorf("%s: %+v", name, got)
		}
	}
}
