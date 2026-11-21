package cygnus

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
	"maple-go-server/internal/skills"
)

var now = time.Unix(100, 0)

// Every thread-C skill of the plan inventory is registered here, and only
// the v148 additions.
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
		if s.Thread == "C" {
			want = append(want, s.ID)
		}
	}
	for _, id := range skills.IDs() {
		if h, _ := skills.Lookup(id); h.Package() == "cygnus" {
			got = append(got, id)
		}
	}
	want = append(want, 14001007, 14101008, 14101009, 14110009, 14110011, 14111007, 14111008, 15000006, 15000008, 15001007, 15101008, 15101010, 15110009, 15110010, 15111008, 15111012)
	sort.Ints(want)
	if len(want) != 132 || !reflect.DeepEqual(got, want) {
		t.Fatalf("registered %d %v\ninventory %d %v", len(got), got, len(want), want)
	}
}

func castCtx(t *testing.T, id, level int, caster domain.Character) *skills.CastContext {
	t.Helper()
	def, err := content.SkillDefinition("", id)
	if err != nil {
		t.Fatal(err)
	}
	v, err := def.Values(level)
	if err != nil {
		t.Fatal(err)
	}
	return &skills.CastContext{Skill: def, Level: level, Values: v, Caster: caster, Now: now}
}

func castAs(t *testing.T, id, level int, caster domain.Character) skills.CastPlan {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	p, err := h.Cast(castCtx(t, id, level, caster))
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func cast(t *testing.T, id, level int) skills.CastPlan {
	ch := domain.Character{HP: 5000, MaxHP: 5000, MP: 5000, MaxMP: 5000, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1452000}}}
	if id == 14111007 {
		ch.Equipment[0].ItemID = 1472000
		ch.Items = []domain.InventoryItem{{InvType: 2, Slot: 1, TemplateID: 2070000, Quantity: 200}}
	}
	return castAs(t, id, level, ch)
}

func stats(b *skills.Buff) map[buffstat.Stat]int {
	out := map[buffstat.Stat]int{}
	for _, s := range b.Stats {
		out[s.Stat] = s.Value
	}
	return out
}

// Every castable skill plans at every level, a buff lasts and uses known
// stats, and party kinds plan party buffs.
func TestEveryCastPlansKnownStats(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 3 }
	for _, id := range skills.IDs() {
		h, _ := skills.Lookup(id)
		if h.Package() != "cygnus" || h.Cast == nil {
			continue
		}
		def, _ := content.SkillDefinition("", id)
		for level := 1; level <= def.MaxLevel; level++ {
			p := cast(t, id, level)
			if p.MPCost < 0 || p.HPCost < 0 || p.Cooldown < 0 {
				t.Errorf("%d@%d: negative cost %+v", id, level, p)
			}
			b := p.Self
			if b == nil {
				b = p.Party
			}
			if (h.Kind&skills.KindPartyBuff != 0) != (p.Party != nil) {
				t.Errorf("%d: party kind and plan differ", id)
			}
			if b == nil {
				if h.Kind&(skills.KindSpecial|skills.KindMobStatus) == 0 {
					t.Errorf("%d@%d: plan does nothing", id, level)
				}
				continue
			}
			if len(b.Stats) == 0 || b.Duration < time.Second {
				t.Errorf("%d@%d: empty buff %+v", id, level, b)
			}
			for _, s := range b.Stats {
				if _, ok := buffstat.Lookup(s.Stat); !ok {
					t.Errorf("%d: unknown stat %s", id, s.Stat)
				}
			}
		}
	}
}

func TestBuffValues(t *testing.T) {
	cases := []struct {
		id, level int
		want      map[buffstat.Stat]int
		duration  time.Duration
		mp        int
	}{
		{11001001, 10, map[buffstat.Stat]int{"WDEF": 200}, 200 * time.Second, 14},
		{11101001, 20, map[buffstat.Stat]int{"BOOSTER": -2}, 200 * time.Second, 11},
		{11101002, 20, map[buffstat.Stat]int{"FINAL_SHOOT_ATTACK": 1}, 650 * time.Second, 25},
		{11101006, 20, map[buffstat.Stat]int{"POWERGUARD": 30}, 180 * time.Second, 20},
		{11111001, 20, map[buffstat.Stat]int{"COMBO": 1}, 200 * time.Second, 32},
		{11111007, 10, map[buffstat.Stat]int{"WK_CHARGE": 1, "MATK": 40}, 300 * time.Second, 40},
		{12001001, 15, map[buffstat.Stat]int{"MAGIC_GUARD": 85}, 600 * time.Second, 13},
		{12001002, 15, map[buffstat.Stat]int{"WDEF": 120, "MDEF": 225}, 240 * time.Second, 16},
		{12101004, 20, map[buffstat.Stat]int{"BOOSTER": -3}, 200 * time.Second, 20},
		{12101005, 10, map[buffstat.Stat]int{"ELEMENT_RESET": 100}, 300 * time.Second, 50},
		{13101001, 20, map[buffstat.Stat]int{"BOOSTER": -2}, 200 * time.Second, 11},
		{13101003, 10, map[buffstat.Stat]int{"SOULARROW": 1}, 300 * time.Second, 15},
		{13101006, 10, map[buffstat.Stat]int{"WIND_WALK": 1}, 30 * time.Second, 25},
		{13101006, 1, map[buffstat.Stat]int{"WIND_WALK": 1, "SPEED": -18}, 12 * time.Second, 21},
		{13111001, 20, map[buffstat.Stat]int{"CONCENTRATE": 25, "ENHANCED_WATK": 20}, 180 * time.Second, 50},
		{14001003, 10, map[buffstat.Stat]int{"DARK_SIGHT": 1}, 200 * time.Second, 5},
		{14101002, 20, map[buffstat.Stat]int{"BOOSTER": -2}, 200 * time.Second, 11},
		{14111000, 20, map[buffstat.Stat]int{"SHADOWPARTNER": 50}, 180 * time.Second, 80},
		{15001003, 10, map[buffstat.Stat]int{"DASH_SPEED": 30, "DASH_JUMP": 20}, 20 * time.Second, 5},
		{15101002, 20, map[buffstat.Stat]int{"BOOSTER": -2}, 200 * time.Second, 11},
		{15111006, 20, map[buffstat.Stat]int{"SPARK": 1}, 240 * time.Second, 40},
	}
	for _, c := range cases {
		p := cast(t, c.id, c.level)
		if p.Self == nil || p.Party != nil || !reflect.DeepEqual(stats(p.Self), c.want) || p.Self.Duration != c.duration || p.MPCost != c.mp {
			t.Errorf("%d@%d: %+v %+v", c.id, c.level, p, p.Self)
		}
	}
	if p := cast(t, 14111000, 1); len(p.Items) != 1 || p.Items[0] != (skills.ItemCost{ItemID: 4006001, Quantity: 1}) {
		t.Errorf("shadow partner items %+v", p.Items)
	}
	if p := cast(t, 11111001, 20); p.Toggle || p.Cooldown != 0 {
		t.Errorf("combo %+v", p)
	}
}

func TestPartyBuffs(t *testing.T) {
	cases := []struct {
		id, level int
		want      map[buffstat.Stat]int
		hp        int
	}{
		{11101003, 20, map[buffstat.Stat]int{"WATK": 30}, 0},
		{12101000, 20, map[buffstat.Stat]int{"MATK": 30}, 0},
		{14101003, 20, map[buffstat.Stat]int{"SPEED": 40, "JUMP": 20}, 0},
		{15101006, 20, map[buffstat.Stat]int{"WK_CHARGE": 1, "MATK": 40}, 0},
		{15111005, 20, map[buffstat.Stat]int{"SPEED_INFUSION": -2}, 40},
	}
	for _, c := range cases {
		p := cast(t, c.id, c.level)
		if p.Self != nil || p.Party == nil || p.Area == nil || p.Area.Left != -p.Area.Right || !reflect.DeepEqual(stats(p.Party), c.want) || p.HPCost != c.hp {
			t.Errorf("%d: %+v %+v", c.id, p, p.Party)
		}
	}
	// Soul Charge and Elemental Reset have lt/rb but stay self-only.
	for _, id := range []int{11111007, 12101005} {
		if p := cast(t, id, 1); p.Party != nil {
			t.Errorf("%d is a party buff", id)
		}
	}
}

func TestSoulArrowNeedsABow(t *testing.T) {
	h, _ := skills.Lookup(13101003)
	if _, err := h.Cast(castCtx(t, 13101003, 1, domain.Character{Equipment: []domain.Equipment{{Slot: -11, ItemID: 1472000}}})); err == nil {
		t.Fatal("soul arrow without a bow")
	}
}

func TestMorphs(t *testing.T) {
	p := castAs(t, 15111002, 5, domain.Character{Gender: 0})
	if got := stats(p.Self); got["STANCE"] != 100 || got["MORPH"] != 1000 || got["ENHANCED_WATK"] != 20 || got["ENHANCED_WDEF"] != 200 || got["SPEED"] != 40 || got["JUMP"] != 20 || p.Cooldown != 200*time.Second {
		t.Errorf("transformation %+v %+v", p, got)
	}
	p = castAs(t, 13111005, 5, domain.Character{Gender: 1})
	if got := stats(p.Self); got["MORPH"] != 1103 || got["ANGEL_ATK"] != 20 || got["ENHANCED_MDEF"] != 100 || got["STANCE"] != 100 || p.Self.Duration != 120*time.Second {
		t.Errorf("eagle eye %+v", got)
	}
}

func TestTeleportMasteryAndDice(t *testing.T) {
	p := cast(t, 12111007, 1)
	if !p.Toggle || p.MPCost != 20 || p.Self.Duration != skills.Permanent || stats(p.Self)["TELEPORT_MASTERY"] != 20 {
		t.Errorf("teleport mastery %+v", p)
	}
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 1 }
	if p := cast(t, 15111011, 1); p.Self != nil || p.MPCost != 60 || p.Cooldown != 200*time.Second {
		t.Errorf("dice 1 %+v", p)
	}
	rollDie = func(int) int { return 4 }
	if p := cast(t, 15111011, 1); p.Self == nil || stats(p.Self)["DICE_ROLL"] != 4 || p.Self.Duration != 180*time.Second {
		t.Errorf("dice 4 %+v", p)
	}
}

func spec(t *testing.T, id int) *combat.AttackSpec {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Attack == nil {
		t.Fatalf("%d has no attack spec", id)
	}
	return h.Attack
}

// Java DamageParse/PlayerHandler decide the packet of each attack.
func TestAttackPackets(t *testing.T) {
	kinds := map[combat.AttackKind][]int{
		combat.Melee: {11001002, 11001003, 11101002, 11111002, 11111003, 11111004, 11111006, 14001002, 14111001, 14111006,
			15001001, 15001002, 15001007, 15101003, 15101005, 15101010, 15111001, 15111003, 15111004, 15111012},
		combat.Shoot: {11101004, 13001003, 13101002, 13101005, 13101007, 13111000, 13111002, 13111006, 13111007,
			14001004, 14101008, 14101009, 14111002, 14111005, 14111008, 15111007, 15111008},
		combat.Magic:                {12001003, 12101002, 12101006, 12111003, 12111005, 12111006},
		combat.Magic | combat.Body:  {12111007},
		combat.Melee | combat.Shoot: {14101006, 15111006},
		combat.Summoned:             {11001004, 12001004, 12111004, 13001004, 14001005, 14111010, 15001004},
	}
	n := 0
	for kind, ids := range kinds {
		for _, id := range ids {
			n++
			if s := spec(t, id); s.Kind != kind {
				t.Errorf("%d: kind %b, want %b", id, s.Kind, kind)
			}
			if h, _ := skills.Lookup(id); kind != combat.Summoned && h.Kind&skills.KindAttack == 0 {
				t.Errorf("%d: not an attack kind", id)
			}
		}
	}
	count := 0
	for _, id := range skills.IDs() {
		if h, _ := skills.Lookup(id); h.Package() == "cygnus" && h.Attack != nil && h.Attack.Kind != 0 {
			count++
		}
	}
	if count != n {
		t.Fatalf("%d attack specs, %d listed", count, n)
	}
	for _, id := range []int{11101004, 13101005, 13111000, 13111007, 14101006, 15111006, 15111007} {
		if spec(t, id).Ammo != combat.AmmoNone {
			t.Errorf("%d consumes ammo", id)
		}
	}
	for _, id := range []int{13001003, 13101007, 14001004, 14101008, 14101009, 14111002} {
		if spec(t, id).Ammo != combat.AmmoAuto {
			t.Errorf("%d does not consume ammo", id)
		}
	}
}

func prepared(t *testing.T, id int, ch domain.Character, e combat.Effect) (*combat.AttackContext, error) {
	t.Helper()
	s := spec(t, id)
	ctx := &combat.AttackContext{Kind: combat.Melee, Character: ch, Effect: e, Now: now}
	if s.Prepare == nil {
		return ctx, nil
	}
	return ctx, s.Prepare(ctx)
}

func TestV148ArrowRainStun(t *testing.T) {
	const arrowRain = 13111000
	h, ok := skills.Lookup(arrowRain)
	if !ok || h.Kind&skills.KindMobStatus == 0 || len(h.Attack.OnHit) != 1 {
		t.Fatalf("Arrow Rain is missing its on-hit monster status: %+v", h)
	}
	apply := h.Attack.OnHit[0]
	effect := combat.Effect{SkillID: arrowRain, Prop: 60, Time: 4 * time.Second}
	var chance int
	status, ok := combat.Resolve(apply, effect, 1, 0, now, func(prop int) bool {
		chance = prop
		return true
	})
	if !ok || chance != 60 || status.Stat != combat.MobStun || status.SkillID != arrowRain || status.ExpiresAt != now.Add(4*time.Second) {
		t.Fatalf("rank 20 Arrow Rain stun = %+v, chance %d, applied %t", status, chance, ok)
	}
	if _, ok := combat.Resolve(apply, effect, 1, 0, now, func(int) bool { return false }); ok {
		t.Fatal("a missed stun roll applied a status")
	}
	if _, ok := combat.Resolve(apply, combat.Effect{SkillID: arrowRain}, 1, 0, now, nil); ok {
		t.Fatal("an effect with no duration applied a status")
	}
}

func withBuff(id int, stat string, value int) domain.Character {
	return domain.Character{SkillBuffs: []domain.SkillBuff{{ID: id, Stat: stat, Value: value, ExpiresAt: now.Add(time.Minute)}}}
}

func TestElements(t *testing.T) {
	cases := []struct {
		id     int
		ch     domain.Character
		effect combat.Effect
		want   combat.Element
	}{
		{11001002, domain.Character{}, combat.Effect{}, combat.Physical},
		{11001002, withBuff(11111007, "WK_CHARGE", 1), combat.Effect{}, combat.Holy},
		{11101004, withBuff(11111007, "WK_CHARGE", 1), combat.Effect{}, combat.Holy},
		{15001001, withBuff(15101006, "WK_CHARGE", 1), combat.Effect{}, combat.Lightning},
		{15111007, withBuff(15101006, "WK_CHARGE", 1), combat.Effect{}, combat.Lightning},
		{15001001, withBuff(11111007, "WK_CHARGE", 1), combat.Effect{}, combat.Physical},
		{15111006, domain.Character{}, combat.Effect{}, combat.Lightning},
		{12101002, domain.Character{}, combat.Effect{}, combat.Fire},
		{12101002, withBuff(12101005, "ELEMENT_RESET", 100), combat.Effect{}, combat.Physical},
		{12111007, domain.Character{}, combat.Effect{}, combat.Physical},
		{11001004, domain.Character{}, combat.Effect{}, combat.Holy},
		{12001004, domain.Character{}, combat.Effect{}, combat.Fire},
		{12111004, domain.Character{}, combat.Effect{}, combat.Fire},
		{15001004, domain.Character{}, combat.Effect{}, combat.Lightning},
		{13001004, domain.Character{}, combat.Effect{}, combat.Physical},
	}
	for _, c := range cases {
		ctx, err := prepared(t, c.id, c.ch, c.effect)
		if err != nil || ctx.Effect.Element != c.want {
			t.Errorf("%d: %v %v", c.id, ctx.Effect.Element, err)
		}
	}
}

func TestDotAttacksPoison(t *testing.T) {
	ctx, _ := prepared(t, 12111003, domain.Character{}, combat.Effect{DOT: 100, DOTTime: 5 * time.Second})
	if len(ctx.Effect.MonsterStatus) != 1 || ctx.Effect.MonsterStatus[0] != poison {
		t.Errorf("meteor %+v", ctx.Effect.MonsterStatus)
	}
	ctx, _ = prepared(t, 12101002, domain.Character{}, combat.Effect{})
	if len(ctx.Effect.MonsterStatus) != 0 {
		t.Errorf("fire arrow %+v", ctx.Effect.MonsterStatus)
	}
	for _, id := range []int{12111005, 14111006} {
		f := spec(t, id).Field
		if f == nil || f.Kind != combat.FieldArea || !f.Box.Set || len(f.MobStatus) != 1 || f.MobStatus[0] != poison {
			t.Errorf("%d mist %+v", id, f)
		}
	}
	if got := spec(t, 14111006).OnHit; !reflect.DeepEqual(got, []combat.MobStatusApply{poison}) {
		t.Errorf("poison bomb %+v", got)
	}
}

func TestMonsterStatuses(t *testing.T) {
	if got := spec(t, 11111002).OnHit; !reflect.DeepEqual(got, []combat.MobStatusApply{darkness}) {
		t.Errorf("panic %+v", got)
	}
	for _, id := range []int{11111003, 15101005} {
		if got := spec(t, id).OnHit; !reflect.DeepEqual(got, []combat.MobStatusApply{stun}) {
			t.Errorf("%d %+v", id, got)
		}
	}
	if got := spec(t, 14001002).OnHit; len(got) != 2 || got[0].Stat != combat.MobPAD || got[0].ValueFrom != "x" || got[1].Stat != combat.MobPDR || got[1].ValueFrom != "y" {
		t.Errorf("disorder %+v", got)
	}
	if got := spec(t, 14111001).OnHit; !reflect.DeepEqual(got, []combat.MobStatusApply{shadowWeb}) {
		t.Errorf("shadow web 14111001 %+v", got)
	}
	casts := map[int]combat.MobStatusApply{
		11111008: {Stat: combat.MobMagicCrash, Value: 1},
		12101001: {Stat: combat.MobSpeed, ValueFrom: "x"},
		12111002: {Stat: combat.MobSeal, Value: 1},
	}
	for id, want := range casts {
		s := spec(t, id)
		if s.Kind != 0 || s.MobCast == nil || !s.MobCast.UseBox || !reflect.DeepEqual(s.MobCast.Statuses, []combat.MobStatusApply{want}) {
			t.Errorf("%d %+v", id, s.MobCast)
		}
		if id == 11111008 && !s.MobCast.Dispel {
			t.Errorf("11111008 magic crash missing dispel")
		}
		if p := cast(t, id, 1); p.Self != nil || p.MPCost < 1 {
			t.Errorf("%d cast %+v", id, p)
		}
	}
}

func TestSummons(t *testing.T) {
	for _, id := range []int{11001004, 12001004, 12111004, 13001004, 14001005, 15001004} {
		s := spec(t, id)
		if s.Summon == nil || s.Summon.MoveAbility != follow || s.Summon.AssistType != 1 || s.Summon.Puppet {
			t.Errorf("%d %+v", id, s.Summon)
		}
		if p := cast(t, id, 1); stats(p.Self)["SUMMON"] != 1 || len(p.Self.Stats) != 1 {
			t.Errorf("%d buff %+v", id, p.Self)
		}
	}
	s := spec(t, 13111004)
	if s.Kind != 0 || s.Summon == nil || !s.Summon.Puppet || s.Summon.MoveAbility != stationary {
		t.Errorf("puppet %+v", s.Summon)
	}
	if p := cast(t, 13111004, 15); stats(p.Self)["PUPPET"] != 1 || p.Self.Duration != 60*time.Second {
		t.Errorf("puppet buff %+v", p.Self)
	}
	df := spec(t, 14111010)
	if df.Kind != combat.Summoned || df.Summon == nil || df.Summon.MoveAbility != stationary || df.Summon.AssistType != 7 {
		t.Errorf("dark flare 14111010 %+v", df.Summon)
	}
	if p := cast(t, 14111010, 1); p.MPCost != 100 || p.Cooldown != 60*time.Second || p.Self == nil || p.Self.Duration != 60*time.Second || stats(p.Self)["SUMMON"] != 1 {
		t.Errorf("dark flare 14111010 cast %+v %+v", p, p.Self)
	}
	if h, ok := skills.Lookup(15001007); !ok || h.LevelFrom != 15101003 || h.Attack == nil || h.Attack.Kind != combat.Melee {
		t.Errorf("dash corkscrew 15001007 handler %+v", h)
	}
}

func TestFinishersNeedOrbs(t *testing.T) {
	for _, id := range []int{11111002, 11111003} {
		if _, err := prepared(t, id, domain.Character{}, combat.Effect{Damage: 500}); err == nil {
			t.Errorf("%d without combo", id)
		}
		if _, err := prepared(t, id, withBuff(11111001, "COMBO", 1), combat.Effect{Damage: 500}); err == nil {
			t.Errorf("%d with no orbs", id)
		}
		ctx, err := prepared(t, id, withBuff(11111001, "COMBO", 3), combat.Effect{Damage: 500})
		if err != nil || ctx.Effect.Damage != 1000 {
			t.Errorf("%d with 2 orbs: %d %v", id, ctx.Effect.Damage, err)
		}
	}
}

func trigger(t *testing.T, id, level int, ch domain.Character, event *combat.AttackEvent) *skills.BuffChange {
	t.Helper()
	h, _ := skills.Lookup(id)
	c := castCtx(t, id, level, ch)
	ctx := &skills.TriggerContext{Skill: c.Skill, Level: level, Values: c.Values, Character: ch, Attack: event, Now: now}
	for _, b := range ch.SkillBuffs {
		if b.ID == id && b.ExpiresAt.After(now) {
			ctx.Rows = append(ctx.Rows, b)
		}
	}
	return h.OnAttack(ctx)
}

func TestComboOrbs(t *testing.T) {
	defer func(orig func(int) bool) { roll = orig }(roll)
	roll = func(int) bool { return true }
	orbs := func(value, attack int, adv int) int {
		ch := withBuff(11111001, "COMBO", value)
		ch.Job = 1111
		ch.Skills = []domain.SkillState{{ID: 11111001, Level: 20}}
		if adv > 0 {
			ch.Skills = append(ch.Skills, domain.SkillState{ID: 11110005, Level: adv})
		}
		c := trigger(t, 11111001, 20, ch, &combat.AttackEvent{SkillID: attack, Targets: []int32{1}})
		if c == nil {
			return value
		}
		if c.Buff.Duration != time.Minute {
			t.Fatalf("combo duration %v", c.Buff.Duration)
		}
		return c.Buff.Stats[0].Value
	}
	cases := []struct{ value, attack, adv, want int }{
		{1, 11001002, 0, 2}, {5, 11001002, 0, 6}, {6, 11001002, 0, 6}, // level 20 x = 5 orbs
		{4, 11111001, 0, 4}, {4, 11111002, 0, 3}, {4, 11111003, 0, 2}, {2, 11111003, 0, 1},
		{1, 11001002, 20, 3}, {10, 11001002, 20, 11}, {11, 11001002, 20, 11}, // advanced combo: x 10 orbs, prop doubles
	}
	for _, c := range cases {
		if got := orbs(c.value, c.attack, c.adv); got != c.want {
			t.Errorf("%+v: %d", c, got)
		}
	}
	// Live train 22: a leftover COMBO kept growing after QA !job 1211.
	ch := withBuff(11111001, "COMBO", 4)
	ch.Job = 1211
	ch.Skills = []domain.SkillState{{ID: 11111001, Level: 20}}
	if c := trigger(t, 11111001, 20, ch, &combat.AttackEvent{SkillID: 12001003, Targets: []int32{1}}); c != nil {
		t.Errorf("combo grew on job 1211: %+v", c)
	}
}

func TestEnergyCharge(t *testing.T) {
	ch := domain.Character{Job: 1511, Skills: []domain.SkillState{{ID: 15100004, Level: 20}}}
	ev := &combat.AttackEvent{SkillID: 15001001, Targets: []int32{1, 2}, Lines: 3}
	c := trigger(t, 15100004, 20, ch, ev)
	if c == nil || stats(c.Buff)["ENERGY_CHARGE"] != 0 || c.Buff.Duration != skills.Permanent {
		t.Fatalf("start %+v", c)
	}
	ch.SkillBuffs = []domain.SkillBuff{{ID: 15100004, Stat: "ENERGY_CHARGE", Value: 9000, ExpiresAt: now.Add(skills.Permanent)}}
	if c = trigger(t, 15100004, 20, ch, ev); stats(c.Buff)["ENERGY_CHARGE"] != energyFull {
		t.Fatalf("fill %+v", c)
	}
	ch.SkillBuffs[0].Value = energyFull
	if c = trigger(t, 15100004, 20, ch, ev); c.Buff.Duration != 60*time.Second {
		t.Fatalf("full %+v", c)
	}
	ch.SkillBuffs[0].ExpiresAt = now.Add(30 * time.Second)
	if c = trigger(t, 15100004, 20, ch, ev); c != nil {
		t.Fatalf("timed %+v", c)
	}
	ch.Job = 512
	ch.SkillBuffs = nil
	if c = trigger(t, 15100004, 20, ch, ev); c != nil {
		t.Fatal("non thunder breaker")
	}
}

func TestV148EnergyBlastRequiresItsOwnFullGauge(t *testing.T) {
	s := spec(t, 15111008)
	ctx := &combat.AttackContext{Now: now, Stats: combat.PlayerStats{WeaponID: 1482000}}
	if !s.Allows(combat.Shoot) || s.Allows(combat.Melee) || s.Ammo != combat.AmmoNone || s.Prepare(ctx) == nil {
		t.Fatal("Energy Blast allowed the wrong attack family, ammo rule or missing gauge")
	}
	ctx.Character.SkillBuffs = []domain.SkillBuff{{ID: 15100004, Stat: "ENERGY_CHARGE", Value: 9999, ExpiresAt: now.Add(time.Minute)}}
	if s.Prepare(ctx) == nil {
		t.Fatal("partial gauge allowed")
	}
	ctx.Character.SkillBuffs[0].Value = energyFull
	if err := s.Prepare(ctx); err != nil {
		t.Fatalf("full gauge rejected: %v", err)
	}
	ctx.Character.SkillBuffs[0].ID = 5110001
	if s.Prepare(ctx) == nil {
		t.Fatal("another job's retained energy gauge allowed")
	}
}

func TestStealthBreaksOnHit(t *testing.T) {
	for _, id := range []int{13101006, 14001003} {
		ch := withBuff(id, "DARK_SIGHT", 1)
		if c := trigger(t, id, 1, ch, &combat.AttackEvent{SkillID: 14001004, Targets: []int32{7}}); c == nil || !c.Cancel {
			t.Errorf("%d not cancelled", id)
		}
		if c := trigger(t, id, 1, ch, &combat.AttackEvent{SkillID: 14001004}); c != nil {
			t.Errorf("%d cancelled by a miss", id)
		}
		if c := trigger(t, id, 1, ch, &combat.AttackEvent{SkillID: 14001005, Summon: true, Targets: []int32{7}}); c != nil {
			t.Errorf("%d cancelled by a summon", id)
		}
	}
}

func passiveBonus(t *testing.T, id, level int, ch domain.Character) skills.Bonus {
	t.Helper()
	h, _ := skills.Lookup(id)
	c := castCtx(t, id, level, ch)
	return h.Passive(&skills.PassiveContext{Skill: c.Skill, Level: level, Values: c.Values, Character: ch, Now: now})
}

func weapon(id int) domain.Character {
	return domain.Character{Equipment: []domain.Equipment{{Slot: -11, ItemID: id}}}
}

func TestPassives(t *testing.T) {
	cases := []struct {
		id, level int
		ch        domain.Character
		want      skills.Bonus
	}{
		{11000005, 10, domain.Character{}, skills.Bonus{MaxHPPercent: 20}},
		{12000005, 10, domain.Character{}, skills.Bonus{MaxMPPercent: 20}},
		{15100007, 10, domain.Character{}, skills.Bonus{MaxHPPercent: 20}},
		{11100000, 20, weapon(1402000), skills.Bonus{Mastery: 50, ACC: 120}},
		{11100000, 20, weapon(1432000), skills.Bonus{}},
		{12100007, 20, weapon(1372000), skills.Bonus{Mastery: 50, MATK: 10}},
		{13100000, 20, weapon(1452000), skills.Bonus{Mastery: 50, ACC: 120}},
		{14100000, 20, weapon(1472000), skills.Bonus{Mastery: 50, ACC: 120}},
		{15100001, 20, weapon(1482000), skills.Bonus{Mastery: 50, ACC: 120}},
		{15100001, 20, weapon(1492000), skills.Bonus{}},
		{11100007, 10, domain.Character{}, skills.Bonus{STR: 30, DEX: 30}},
		{14100010, 10, domain.Character{}, skills.Bonus{LUK: 30, DEX: 30}},
		{12100008, 10, domain.Character{}, skills.Bonus{INT: 40}},
		{11110000, 20, domain.Character{}, skills.Bonus{HPRecovery: 100, MPRecovery: 60}},
		{12110000, 20, domain.Character{}, skills.Bonus{Critical: 30, CriticalDamageMin: 25}},
		{12110001, 20, domain.Character{}, skills.Bonus{MPConsume: 50, ElementAmp: 50}},
		{12111004, 20, domain.Character{}, skills.Bonus{TER: 30}},
		{13000000, 20, domain.Character{}, skills.Bonus{Critical: 40}},
		{13000001, 15, domain.Character{}, skills.Bonus{ACC: 225, AVOID: 225, Speed: 30, Range: 120, SpeedLimitBonus: 20}},
		{13110003, 20, weapon(1452000), skills.Bonus{Mastery: 65, WATK: 25, CriticalDamageMin: 15}},
		{13110008, 10, domain.Character{}, skills.Bonus{AvoidRate: 30}},
		{14000000, 20, domain.Character{}, skills.Bonus{ACC: 160, AVOID: 160}},
		{14000001, 5, domain.Character{}, skills.Bonus{Range: 200}},
		{14100001, 20, weapon(1472000), skills.Bonus{Critical: 50}},
		{14100001, 20, weapon(1472001), skills.Bonus{Critical: 50}},
		{14100001, 20, weapon(1332000), skills.Bonus{}},
		{14100001, 20, domain.Character{}, skills.Bonus{}},
		{14110003, 20, domain.Character{}, skills.Bonus{PotionPercent: 50}},
		{15000000, 20, domain.Character{}, skills.Bonus{ACC: 160, AVOID: 160}},
		{15110000, 15, domain.Character{}, skills.Bonus{Critical: 30, CriticalDamageMin: 15}},
		{15110009, 20, domain.Character{Job: 1511}, skills.Bonus{Critical: 15, CriticalDamageMin: 10, CriticalDamage: 10, BossCritical: 20}},
		{15110010, 20, domain.Character{Job: 1511}, skills.Bonus{StunnedCritical: 60}},
		{15110010, 20, domain.Character{Job: 1111}, skills.Bonus{}},
		{13101006, 10, domain.Character{}, skills.Bonus{}},
		{13101006, 10, withBuff(13101006, "WIND_WALK", 1), skills.Bonus{DamagePercent: 60}},
		{14001003, 1, withBuff(14001003, "DARK_SIGHT", 1), skills.Bonus{DamagePercent: 27}},
	}
	for _, c := range cases {
		if got := passiveBonus(t, c.id, c.level, c.ch); got != c.want {
			t.Errorf("%d@%d: %+v", c.id, c.level, got)
		}
	}
}

func TestVenomAndDrains(t *testing.T) {
	stats := combat.PlayerStats{Job: 1411, LUK: 300, DEX: 50, STR: 4, WATK: 80, WeaponID: 1472000}
	ch := domain.Character{Skills: []domain.SkillState{{ID: 14110004, Level: 20}}}
	wantVenom := combat.MobStatusApply{
		Stat: combat.MobPoison, Poison: true, Prop: 30,
		Value: int(combat.MaxBaseDamage(stats) * 90 / 100), Duration: 6 * time.Second,
	}
	for _, id := range []int{14001004, 14111002, 14111005, 14111008} {
		ctx := &combat.AttackContext{Kind: combat.Shoot, Character: ch, Stats: stats, Now: now}
		if err := spec(t, id).Prepare(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 1 || ctx.Effect.MonsterStatus[0] != wantVenom {
			t.Errorf("%d venom %+v %v", id, ctx.Effect.MonsterStatus, err)
		}
		ctx = &combat.AttackContext{Kind: combat.Shoot, Now: now}
		if _ = spec(t, id).Prepare(ctx); len(ctx.Effect.MonsterStatus) != 0 {
			t.Errorf("%d venom without the skill", id)
		}
	}
	for _, id := range []int{14101006, 15111001} {
		ctx := &combat.AttackContext{Character: domain.Character{MaxHP: 1000}, Effect: combat.Effect{X: 10}}
		if hp, mp := spec(t, id).Drain(ctx, 3000); hp != 300 || mp != 0 {
			t.Errorf("%d drain %d %d", id, hp, mp)
		}
		if hp, _ := spec(t, id).Drain(ctx, 30000); hp != 500 {
			t.Errorf("%d drain cap %d", id, hp)
		}
	}
	if spec(t, 13111006).Drain != nil {
		t.Error("wind piercing drains")
	}
}

func TestNightWalkerShadowStarsCostAndSplit(t *testing.T) {
	const skillID = 14111007
	ch := domain.Character{Equipment: []domain.Equipment{{Slot: -11, ItemID: 1472000}},
		Items: []domain.InventoryItem{
			{InvType: 2, Slot: 5, TemplateID: 2070002, Quantity: 200},
			{InvType: 2, Slot: 1, TemplateID: 2060000, Quantity: 200},
			{InvType: 2, Slot: 3, TemplateID: 2070000, Quantity: 190},
		}}
	p := castAs(t, skillID, 1, ch)
	if p.MPCost != 6 || p.Self == nil || p.Self.Duration != 108*time.Second ||
		!reflect.DeepEqual(p.Items, []skills.ItemCost{{ItemID: 2070000, Quantity: 190}}) ||
		!reflect.DeepEqual(stats(p.Self), map[buffstat.Stat]int{"SPIRIT_CLAW": 1}) {
		t.Fatalf("unexpected Shadow Stars cast: %+v", p)
	}
	ch.Items = ch.Items[1:]
	ch.Items[1].Quantity--
	if _, err := nightShadowStars(castCtx(t, skillID, 1, ch)); err == nil {
		t.Fatal("Shadow Stars accepted fewer than 190 stars")
	}
	ctx := &combat.AttackContext{Kind: combat.Shoot, Character: ch, Now: now}
	split := spec(t, 14111008)
	if err := split.Prepare(ctx); err != nil || ctx.NoAmmo || split.Kind != combat.Shoot {
		t.Fatalf("unbuffed Shadow Split: %+v %v", ctx, err)
	}
	ctx.Character.SkillBuffs = []domain.SkillBuff{{ID: skillID, Stat: "SPIRIT_CLAW", ExpiresAt: now.Add(time.Second)}}
	if err := split.Prepare(ctx); err != nil || !ctx.NoAmmo {
		t.Fatalf("buffed Shadow Split consumed stars: %+v %v", ctx, err)
	}
}

func TestSoulArrowSkipsArrows(t *testing.T) {
	for _, id := range []int{13001003, 13111002} {
		ctx, _ := prepared(t, id, withBuff(13101003, "SOULARROW", 1), combat.Effect{})
		if !ctx.NoAmmo {
			t.Errorf("%d uses arrows under soul arrow", id)
		}
		ctx, _ = prepared(t, id, domain.Character{}, combat.Effect{})
		if ctx.NoAmmo {
			t.Errorf("%d skips arrows", id)
		}
	}
}
