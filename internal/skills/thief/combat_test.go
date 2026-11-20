package thief

import (
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
)

func spec(t *testing.T, id int) *combat.AttackSpec {
	t.Helper()
	h := registered(t, id)
	if h.Attack == nil {
		t.Fatalf("skill %d has no attack spec", id)
	}
	return h.Attack
}

func learnedChar(job int, s ...domain.SkillState) domain.Character {
	return domain.Character{Job: job, MaxHP: 5000, Skills: s}
}

func prepared(t *testing.T, id, level int, kind combat.AttackKind, ch domain.Character, stats combat.PlayerStats) *combat.AttackContext {
	t.Helper()
	def, _ := content.SkillDefinition("", id)
	eff, err := combat.FromSkill(def, level)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &combat.AttackContext{Kind: kind, Character: ch, Stats: stats, Effect: eff, Now: time.Unix(1000, 0)}
	if s := spec(t, id); s.Prepare != nil {
		if err = s.Prepare(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return ctx
}

// Venom poisons only on the Java DamageParse attack list, with the first
// learned Venom's prop, dotTime and dot% of max base damage.
func TestVenomPoisonsListedAttacks(t *testing.T) {
	stats := combat.PlayerStats{Job: 422, LUK: 400, DEX: 50, STR: 4, WATK: 100, WeaponID: 1332000}
	now := time.Unix(1000, 0)
	skill := domain.SkillState{ID: 4220005, Level: 30}
	ch := learnedChar(422, skill)
	if values(ch, now, skill.ID) == nil {
		skill = domain.SkillState{ID: 4210010, Level: 20}
		ch = learnedChar(422, skill)
	}
	wz := values(ch, now, skill.ID)
	if wz == nil {
		t.Fatalf("no configured Venom definition for %d", skill.ID)
	}
	ctx := prepared(t, 4201005, 30, combat.Melee, ch, stats)
	want := int64(wz["dot"]) * combat.MaxBaseDamage(stats) / 100
	if len(ctx.Effect.MonsterStatus) != 1 {
		t.Fatalf("savage blow statuses %+v", ctx.Effect.MonsterStatus)
	}
	got := ctx.Effect.MonsterStatus[0]
	if got.Stat != combat.MobPoison || !got.Poison || int64(got.Value) != want || got.Prop != wz["prop"] || got.Duration != time.Duration(wz["dotTime"])*time.Second {
		t.Fatalf("venom %+v want tick %d", got, want)
	}
	if ctx = prepared(t, 4211004, 20, combat.Melee, ch, stats); len(ctx.Effect.MonsterStatus) != 0 {
		t.Fatalf("band of thieves is not a venom attack: %+v", ctx.Effect.MonsterStatus)
	}
	if ctx = prepared(t, 4201005, 30, combat.Melee, learnedChar(422), stats); len(ctx.Effect.MonsterStatus) != 0 {
		t.Fatal("venom without the skill")
	}
}

// Passive damage increases add to the attack's damage percent.
func TestDamageIncreases(t *testing.T) {
	stats := combat.PlayerStats{Job: 412, LUK: 400, WATK: 100, WeaponID: 1472000}
	now := time.Unix(1000, 0)
	legacyExpert := learnedChar(412, domain.SkillState{ID: 4120010, Level: 10})
	if old := values(legacyExpert, now, 4120010); old != nil {
		base := prepared(t, 4111005, 20, combat.Shoot, learnedChar(412), stats)
		ctx := prepared(t, 4111005, 20, combat.Shoot, legacyExpert, stats)
		if ctx.Effect.Damage != base.Effect.Damage+old["damR"] {
			t.Fatalf("avenger with expert star handling: %d", ctx.Effect.Damage)
		}
	} else {
		v148Expert := learnedChar(412, domain.SkillState{ID: 4110012, Level: 10})
		v148Expert.Equipment = []domain.Equipment{{Slot: -11, ItemID: stats.WeaponID}}
		wz := values(v148Expert, now, 4110012)
		ctx := prepared(t, 4001344, 20, combat.Shoot, v148Expert, stats)
		if wz == nil || ctx.Stats.DamageRate != wz["damR"] {
			t.Fatalf("lucky seven with v148 throwing star mastery: %+v", ctx.Stats)
		}
	}
	ctx := prepared(t, 4201005, 30, combat.Melee, learnedChar(422, domain.SkillState{ID: 4221007, Level: 30}), stats)
	if ctx.Effect.Damage != 90+25 {
		t.Fatalf("savage blow with boomerang step: %d", ctx.Effect.Damage)
	}
	greed := learnedChar(422, domain.SkillState{ID: 4220009, Level: 10})
	base := prepared(t, 4211006, 20, combat.Melee, learnedChar(422), stats)
	ctx = prepared(t, 4211006, 20, combat.Melee, greed, stats)
	if ctx.Effect.Damage != base.Effect.Damage+values(greed, now, 4220009)["x"] {
		t.Fatalf("meso explosion with greed: %d", ctx.Effect.Damage)
	}
}

// Shadow Stars removes star use; Expert Throwing Star Handling rolls its prop
// for Night Lords only; Taunt and Shadow Meso never use stars.
func TestStarRules(t *testing.T) {
	stats := combat.PlayerStats{Job: 412}
	now := time.Unix(1000, 0)
	ch := learnedChar(412)
	ch.SkillBuffs = []domain.SkillBuff{{ID: 4121006, ExpiresAt: now.Add(time.Minute)}}
	if !prepared(t, 4121007, 30, combat.Shoot, ch, stats).NoAmmo {
		t.Fatal("shadow stars still consumes stars")
	}
	defer func(r func(int) bool) { roll = r }(roll)
	roll = func(int) bool { return true }
	expert := domain.SkillState{ID: 4120010, Level: 10}
	foreignJob := 411
	if values(learnedChar(412, expert), now, expert.ID) == nil {
		expert.ID = 4110012
		foreignJob = 422
	}
	if !prepared(t, 4001344, 20, combat.Shoot, learnedChar(412, expert), stats).NoAmmo {
		t.Fatal("expert star handling did not keep the star")
	}
	if prepared(t, 4001344, 20, combat.Shoot, learnedChar(foreignJob, expert), stats).NoAmmo {
		t.Fatal("expert star handling outside the supported job")
	}
	for _, id := range []int{4121003, 4221003, 4111004} {
		if spec(t, id).Ammo != combat.AmmoNone {
			t.Errorf("skill %d uses stars", id)
		}
	}
}

func TestAttackSpecs(t *testing.T) {
	if s := spec(t, 4001344); s.Kind != combat.Shoot {
		t.Fatal("lucky seven is a shoot attack")
	}
	if s := spec(t, 4001002); len(s.OnHit) != 2 || s.OnHit[0].Stat != combat.MobPAD || s.OnHit[1].ValueFrom != "y" {
		t.Fatalf("disorder %+v", s.OnHit)
	}
	for _, id := range []int{4121008, 4201004, 4211002, 4221007, 4331005} {
		if s := spec(t, id); len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobStun {
			t.Errorf("skill %d stun %+v", id, s.OnHit)
		}
	}
	if s := spec(t, 4321002); s.Kind != combat.Melee || !s.IgnoreRange || s.MobCast != nil || len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobDarkness {
		t.Fatalf("flashbang %+v", s)
	}
	if s := spec(t, 4121003); len(s.OnHit) != 3 || s.OnHit[0].Stat != combat.MobShowdown {
		t.Fatalf("taunt %+v", s.OnHit)
	}
	for id, st := range map[int]combat.MobStat{4121004: combat.MobAmbush, 4221004: combat.MobAmbush} {
		s := spec(t, id)
		if s.Kind != 0 || s.MobCast == nil || !s.MobCast.UseBox || s.MobCast.Statuses[0].Stat != st {
			t.Errorf("skill %d monster cast %+v", id, s.MobCast)
		}
	}
	for _, id := range []int{4111007, 4211007} {
		if s := spec(t, id); s.Kind != combat.Summoned || s.Summon == nil || s.Summon.MoveAbility != 0 || s.Summon.AssistType != 7 {
			t.Errorf("dark flare %d %+v", id, s.Summon)
		}
	}
	if s := spec(t, 4341006); s.Kind != 0 || s.Summon == nil || !s.Summon.Puppet {
		t.Fatalf("mirrored target %+v", s.Summon)
	}
	if s := spec(t, 4221006); s.Kind != 0 || s.Field == nil || s.Field.AreaType != protocol.AreaInvincible || !s.Field.PlayersInvincible {
		t.Fatalf("smoke screen %+v", s.Field)
	} else if def, _ := content.SkillDefinition("", 4221006); true {
		r, ok := def.RangeAt(1)
		if b := s.Field.Box; !ok || !b.Set || b.LT != (combat.Point{X: r.LT[0], Y: r.LT[1]}) || b.RB != (combat.Point{X: r.RB[0], Y: r.RB[1]}) {
			t.Fatalf("smoke screen box %+v, WZ %+v", b, r)
		}
	}
	if s := spec(t, 4101005); s.Drain == nil || s.Kind != combat.Shoot || s.Ammo != combat.AmmoNone {
		t.Fatalf("drain %+v", s)
	}
}

func TestDrainHealsXPercentUpToHalfMaxHP(t *testing.T) {
	ctx := &combat.AttackContext{Character: domain.Character{MaxHP: 1000}, Effect: combat.Effect{X: 45}}
	if hp, mp := drain(ctx, 1000); hp != 450 || mp != 0 {
		t.Fatalf("drain %d %d", hp, mp)
	}
	if hp, _ := drain(ctx, 10000); hp != 500 {
		t.Fatalf("drain cap %d", hp)
	}
}

func TestShadowMesoRange(t *testing.T) {
	// Level 20: moneyCon 570, prop 30, x 60, as the client reads them.
	e := combat.Effect{SkillID: 4111004, MoneyCon: 570, Prop: 30, X: 60}
	if low, high := shadowMesoRange(e); low != 114 || high != 547 {
		t.Fatalf("level 20 %d..%d", low, high)
	}
	// Level 1: moneyCon 285, prop 11, x 22: int(142.5) and int(427.5) truncate.
	if low, high := shadowMesoRange(combat.Effect{MoneyCon: 285, Prop: 11, X: 22}); low != 56 || high != 207 {
		t.Fatalf("level 1 %d..%d", low, high)
	}
	if _, high := shadowMesoRange(combat.Effect{MoneyCon: 570, X: 60}); high != 342 {
		t.Fatalf("no proc high %d", high)
	}
	e.Damage = 20 // A passive's damR.
	if low, high := shadowMesoRange(e); low != 136 || high != 657 {
		t.Fatalf("damR %d..%d", low, high)
	}
}

func TestSummonCasts(t *testing.T) {
	p := plan(t, summonBuff, 4111007, 20, domain.Character{})
	if p.MPCost != 100 || p.Cooldown != 60*time.Second || p.Self == nil || p.Self.Duration != 60*time.Second || !reflect.DeepEqual(p.Self.Stats, buff(stat("SUMMON", 1))) {
		t.Fatalf("dark flare %+v %+v", p, p.Self)
	}
	if _, err := mirroredTarget(context(t, 4341006, 30, domain.Character{Job: 434})); err == nil {
		t.Fatal("mirrored target without shadow partner")
	}
	ch := domain.Character{Job: 434, SkillBuffs: []domain.SkillBuff{{ID: 4331002, ExpiresAt: time.Unix(0, 0).Add(time.Minute)}}}
	p = plan(t, mirroredTarget, 4341006, 30, ch)
	if p.MPCost != 135 || p.Self == nil || p.Self.Duration != 90*time.Second || !reflect.DeepEqual(p.Self.Stats, buff(stat("PUPPET", 1))) {
		t.Fatalf("mirrored target %+v %+v", p, p.Self)
	}
}

func trigger(t *testing.T, id, level int, e *combat.AttackEvent) *skills.BuffChange {
	t.Helper()
	ctx := context(t, id, level, domain.Character{})
	return registered(t, id).OnAttack(&skills.TriggerContext{Skill: ctx.Skill, Level: level, Values: ctx.Values, Attack: e, Now: time.Unix(0, 0)})
}

// Final Cut grants the v148 WZ y and time after its own hit. Its attack pays
// the v148 WZ HP ratio.
func TestFinalCutAfterHit(t *testing.T) {
	c := trigger(t, 4341002, 30, &combat.AttackEvent{SkillID: 4341002, Targets: []int32{7}})
	if c == nil || c.Cancel || c.Buff == nil || c.Buff.Duration != 60*time.Second || !reflect.DeepEqual(c.Buff.Stats, buff(stat("FINAL_CUT", 160))) {
		t.Fatalf("final cut %+v", c)
	}
	if trigger(t, 4341002, 30, &combat.AttackEvent{SkillID: 4341002}) != nil || trigger(t, 4341002, 30, &combat.AttackEvent{SkillID: 4341004, Targets: []int32{7}}) != nil {
		t.Fatal("final cut without its own hit")
	}
	if ctx := prepared(t, 4341002, 30, combat.Melee, learnedChar(434), combat.PlayerStats{}); ctx.Effect.HPRatio != 10 {
		t.Fatalf("final cut hp ratio %d", ctx.Effect.HPRatio)
	}
}

func TestDamageOverTimeSources(t *testing.T) {
	for id, want := range map[int]combat.MobStatusApply{
		4121004: {Stat: combat.MobAmbush, Poison: true, TickFrom: "damage"},
		4221004: {Stat: combat.MobAmbush, Poison: true, TickFrom: "damage"},
	} {
		if got := spec(t, id).MobCast.Statuses; len(got) != 1 || got[0] != want {
			t.Errorf("skill %d %+v", id, got)
		}
	}
	if got := spec(t, 4341003).OnHit; len(got) != 1 || got[0].Stat != combat.MobTimeBomb || !got[0].Explode || got[0].TickFrom != "damage" {
		t.Fatalf("monster bomb %+v", got)
	}
	if s := spec(t, 4211006); !s.ConsumeMesos || s.MaxDamage == nil {
		t.Fatal("meso explosion does not consume mesos")
	}
	if s := spec(t, 4341006).Summon; s.HP != 0 {
		t.Fatal("mirrored target HP must come from WZ x")
	}
}

func TestV148ShadowWebPlansMeleeDamageAndWZTicks(t *testing.T) {
	ch := learnedChar(411)
	ch.ID, ch.MP = 7, 500
	stats := combat.PlayerStats{Job: 411, LUK: 400, DEX: 50, STR: 4, WATK: 100, WeaponID: 1472000}
	def, err := content.SkillDefinition(t.TempDir(), 4111003)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := combat.FromSkill(def, 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx := combat.AttackContext{Kind: combat.Melee, Character: ch, Stats: stats, Effect: effect, Now: time.Unix(1000, 0)}
	ctx.Attack = protocol.Attack{
		Opcode: protocol.RecvMeleeAttack, SkillID: 4111003, TByte: 0x11,
		Targets: []protocol.AttackTarget{{ObjectID: 3, Lines: []protocol.AttackLine{{Raw: 100}}}},
	}
	ctx.Targets = []combat.TargetInfo{{ObjectID: 3, X: -10, HP: 100000}}
	p, err := combat.PlanAttack(spec(t, 4111003), ctx, func(prop int) bool { return prop == 80 })
	if err != nil {
		t.Fatal(err)
	}
	if p.Cost.MP != 25 || len(p.Targets) != 1 || p.Targets[0].Total() != 100 || len(p.Targets[0].Statuses) != 1 {
		t.Fatalf("Shadow Web plan %+v", p)
	}
	if s := p.Targets[0].Statuses[0]; s.Stat != combat.MobWeb || s.ExpiresAt.Sub(ctx.Now) != 8*time.Second ||
		s.Interval != time.Second || s.TickDamage != combat.MaxBaseDamage(stats) {
		t.Fatalf("Shadow Web status %+v", s)
	}
}

// Pickpocket drops min(max(line/20000 × x, 1), x) mesos per line near the
// monster, only on the Java attack list and only while the buff is active.
func TestPickpocketDropsMesos(t *testing.T) {
	defer func(r func(int) bool, rr func(int, int) int) { roll, rollRange = r, rr }(roll, rollRange)
	roll = func(int) bool { return true }
	rollRange = func(lo, _ int) int { return lo }
	now := time.Unix(1000, 0)
	ch := learnedChar(422, domain.SkillState{ID: 4211003, Level: 10})
	ch.SkillBuffs = []domain.SkillBuff{{ID: 4211003, Stat: "PICKPOCKET", Value: 200, ExpiresAt: now.Add(time.Minute)}}
	var drops [][3]int64
	e := combat.AttackEvent{SkillID: 4201005, Now: now, Session: domain.CharacterSession{Character: ch},
		Hits:       []combat.HitInfo{{X: 100, Y: 50, Lines: []int{10000, 1, 900000}}},
		SpawnMesos: func(x, y int, a int64) error { drops = append(drops, [3]int64{int64(x), int64(y), a}); return nil }}
	spec(t, 4201005).AfterHit(e)
	if want := [][3]int64{{50, 50, 100}, {50, 50, 1}, {50, 50, 200}}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops %v", drops)
	}
	drops = nil
	e.SkillID = 4201004
	pickpocketDrop(e)
	e.SkillID, e.Session.Character.SkillBuffs = 4201005, nil
	pickpocketDrop(e)
	if len(drops) != 0 {
		t.Fatalf("unexpected drops %v", drops)
	}
}

func triggerFor(t *testing.T, id, level int, ch domain.Character, rows []domain.SkillBuff, e *combat.AttackEvent) *skills.BuffChange {
	t.Helper()
	ctx := context(t, id, level, ch)
	return registered(t, id).OnAttack(&skills.TriggerContext{Skill: ctx.Skill, Level: level, Values: ctx.Values, Character: ch, Rows: rows, Attack: e, Now: time.Unix(0, 0)})
}

// Owl Spirit: a killing Owl Spirit attack grants OWL_SPIRIT y with x attacks
// left on the server-only counter; each later hitting attack spends one.
func TestOwlSpiritCountsAttacks(t *testing.T) {
	ch := domain.Character{Job: 434}
	if c := triggerFor(t, 4331003, 20, ch, nil, &combat.AttackEvent{SkillID: 4331003, Targets: []int32{1}}); c != nil {
		t.Fatalf("owl spirit without a kill %+v", c)
	}
	c := triggerFor(t, 4331003, 20, ch, nil, &combat.AttackEvent{SkillID: 4331003, Targets: []int32{1}, Defeated: []int32{1}})
	if c == nil || c.Buff == nil || c.Buff.Counter != 15 || c.Buff.Duration != skills.Permanent || !reflect.DeepEqual(c.Buff.Stats, buff(stat("OWL_SPIRIT", 130))) {
		t.Fatalf("owl spirit grant %+v", c)
	}
	rows := []domain.SkillBuff{{ID: 4331003, Stat: "OWL_SPIRIT", Value: 120, Counter: 2, ExpiresAt: time.Unix(0, 0).Add(time.Hour)}}
	c = triggerFor(t, 4331003, 20, ch, rows, &combat.AttackEvent{SkillID: 4341004, Targets: []int32{1}})
	if c == nil || c.Buff == nil || c.Buff.Counter != 1 || c.Buff.Stats[0].Value != 120 || c.Buff.Duration != time.Hour {
		t.Fatalf("owl spirit spend %+v", c)
	}
	rows[0].Counter = 1
	if c = triggerFor(t, 4331003, 20, ch, rows, &combat.AttackEvent{SkillID: 4341004, Targets: []int32{1}}); c == nil || !c.Cancel {
		t.Fatalf("owl spirit end %+v", c)
	}
	if c = triggerFor(t, 4331003, 20, ch, nil, &combat.AttackEvent{SkillID: 4341004, Targets: []int32{1}}); c != nil {
		t.Fatalf("owl spirit without a buff %+v", c)
	}
}

func TestLifeDrain(t *testing.T) {
	defer func(r func(int) bool) { roll = r }(roll)
	roll = func(p int) bool { return p == 11 }
	ch := domain.Character{Job: 434, MaxHP: 1000}
	if c := triggerFor(t, 4330007, 10, ch, nil, &combat.AttackEvent{Damage: 1000}); c == nil || c.HealHP != 300 {
		t.Fatalf("life drain %+v", c)
	}
	if c := triggerFor(t, 4330007, 10, ch, nil, &combat.AttackEvent{Damage: 100000}); c == nil || c.HealHP != 500 {
		t.Fatalf("life drain cap %+v", c)
	}
	if c := triggerFor(t, 4330007, 10, domain.Character{Job: 432, MaxHP: 1000}, nil, &combat.AttackEvent{Damage: 1000}); c != nil {
		t.Fatal("life drain below Blade Lord")
	}
}

func TestStealRollsPropOnNonBosses(t *testing.T) {
	defer func(r func(int) bool, b func(int) bool) { roll, mobBoss = r, b }(roll, mobBoss)
	def, err := content.ConfiguredSkillDefinition(4201004)
	if err != nil {
		t.Fatal(err)
	}
	wz, err := def.Values(def.MaxLevel)
	if err != nil {
		t.Fatal(err)
	}
	roll = func(p int) bool { return p == wz["prop"] }
	mobBoss = func(id int) bool { return id == 9 }
	var stolen []int32
	e := combat.AttackEvent{SkillID: 4201004, Now: time.Unix(1000, 0),
		Session:   domain.CharacterSession{Character: learnedChar(422, domain.SkillState{ID: 4201004, Level: def.MaxLevel})},
		Hits:      []combat.HitInfo{{ObjectID: 1, MobID: 100100}, {ObjectID: 2, MobID: 9}},
		StealItem: func(h combat.HitInfo) error { stolen = append(stolen, h.ObjectID); return nil }}
	spec(t, 4201004).AfterHit(e)
	if !reflect.DeepEqual(stolen, []int32{1}) {
		t.Fatalf("stolen %v", stolen)
	}
}

func TestTornadoSpinIsSendable(t *testing.T) {
	p := plan(t, registered(t, 4321000).Cast, 4321000, 20, domain.Character{})
	names := make([]buffstat.Stat, 0, len(p.Self.Stats))
	for _, s := range p.Self.Stats {
		names = append(names, s.Stat)
	}
	if _, err := buffstat.Resolve(names); err != nil {
		t.Fatal(err)
	}
}

// Dark Sight ends on a hitting attack; Advanced Dark Sight keeps it with its
// prop (Java afterAttack).
func TestDarkSightBreaksOnAttack(t *testing.T) {
	defer func(r func(int) bool) { roll = r }(roll)
	rows := []domain.SkillBuff{{ID: 4001003, Stat: "DARK_SIGHT", Value: 1, ExpiresAt: time.Unix(0, 0).Add(time.Minute)}}
	hit := &combat.AttackEvent{SkillID: 4001334, Targets: []int32{1}}
	if c := triggerFor(t, 4001003, 10, learnedChar(412), rows, hit); c == nil || !c.Cancel {
		t.Fatalf("dark sight kept %+v", c)
	}
	if c := triggerFor(t, 4001003, 10, learnedChar(412), rows, &combat.AttackEvent{SkillID: 4001334}); c != nil {
		t.Fatal("a miss ended dark sight")
	}
	blade := learnedChar(434, domain.SkillState{ID: 4330001, Level: 10})
	roll = func(p int) bool { return p == 49 }
	if c := triggerFor(t, 4001003, 10, blade, rows, hit); c != nil {
		t.Fatalf("advanced dark sight did not keep it %+v", c)
	}
	roll = func(int) bool { return false }
	if c := triggerFor(t, 4001003, 10, blade, rows, hit); c == nil || !c.Cancel {
		t.Fatal("advanced dark sight failed roll kept it")
	}
}
