package pirate

import (
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

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

func cast(t *testing.T, id, level int) skills.CastPlan {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Cast == nil {
		t.Fatalf("skill %d not castable", id)
	}
	def, v := values(t, id, level)
	caster := domain.Character{HP: 5000, MaxHP: 5000, MP: 5000, MaxMP: 5000}
	switch id {
	case 5120012:
		caster.Skills = []domain.SkillState{{ID: 5111007, Level: 20}}
	case 5220014:
		caster.Skills = []domain.SkillState{{ID: 5211007, Level: 20}}
	}
	p, err := h.Cast(&skills.CastContext{Skill: def, Level: level, Values: v, Caster: caster, Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("skill %d: %v", id, err)
	}
	return p
}

func stats(b *skills.Buff) map[buffstat.Stat]int {
	out := map[buffstat.Stat]int{}
	for _, s := range b.Stats {
		out[s.Stat] = s.Value
	}
	return out
}

// Every registered pirate skill is in the WZ seed, every castable one plans
// at every level, and every stat it plans is a known buff stat.
func TestEveryPirateHandlerPlansKnownStats(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 2 }
	count := 0
	for _, id := range skills.IDs() {
		if skills.OwnerOf(id) != "pirate" {
			continue
		}
		count++
		h, _ := skills.Lookup(id)
		def, err := content.SkillDefinition("", id)
		if err != nil {
			t.Fatalf("%d: %v", id, err)
		}
		if h.Attack != nil && h.Attack.MobCast == nil && id != 5221009 { // Mind Control has no WZ damage (combat defaults to 100%)
			if _, v := values(t, id, def.MaxLevel); v["damage"] < 1 {
				t.Errorf("%d: attack without damage", id)
			}
		}
		if h.Cast == nil {
			continue
		}
		for level := 1; level <= def.MaxLevel; level++ {
			p := cast(t, id, level)
			if p.MPCost < 0 || p.HPCost < 0 || p.Cooldown < 0 {
				t.Errorf("%d@%d: negative cost %+v", id, level, p)
			}
			buff := p.Self
			if buff == nil {
				buff = p.Party
			}
			effect := p.Dispel || p.Heal != nil || p.ResetCooldowns || h.Kind&skills.KindSpecial != 0 || h.Attack != nil && h.Attack.MobCast != nil
			if buff == nil && !effect || buff != nil && len(buff.Stats) == 0 && !effect || buff != nil && len(buff.Stats) > 0 && buff.Duration < time.Second {
				t.Fatalf("%d@%d: plan does nothing %+v", id, level, p)
			}
			if buff == nil {
				continue
			}
			if (h.Kind&skills.KindPartyBuff != 0) != (p.Party != nil) {
				t.Errorf("%d: party kind and plan differ", id)
			}
			for _, s := range buff.Stats {
				if _, ok := buffstat.Lookup(s.Stat); !ok {
					t.Errorf("%d: unknown stat %s", id, s.Stat)
				}
			}
		}
	}
	if count != 140 { // 93 from v145 and 47 from the v148 books
		t.Fatalf("registered %d pirate skills", count)
	}
}

func TestV148PirateRegionalPartyCasts(t *testing.T) {
	for _, id := range []int{5721000, 5821000, 5921000} {
		p := cast(t, id, 30)
		if p.Self != nil || p.Party == nil || p.Area == nil ||
			p.Area.Left != -400 || p.Area.Top != -300 || p.Area.Right != 400 || p.Area.Bottom != 300 ||
			stats(p.Party)["MAPLE_WARRIOR"] != 15 || p.Party.Duration != 900*time.Second || p.MPCost != 70 {
			t.Errorf("%d: Maple Warrior plan %+v, buff %+v", id, p, p.Party)
		}
	}
	for _, tc := range []struct {
		id, level int
		cooldown  time.Duration
	}{{5121010, 5, 1500 * time.Second}, {5821010, 30, 1200 * time.Second}} {
		p := cast(t, tc.id, tc.level)
		if p.Self != nil || p.Party == nil || !p.ResetCooldowns ||
			len(p.KeepCooldowns) != 2 || p.KeepCooldowns[0] != 5121010 || p.KeepCooldowns[1] != 5821010 ||
			p.Cooldown != tc.cooldown || p.Area == nil ||
			p.Area.Left != -400 || p.Area.Top != -300 || p.Area.Right != 400 || p.Area.Bottom != 300 {
			t.Errorf("%d: Time Leap plan %+v", tc.id, p)
		}
	}
}

func TestBoosterMapleWarriorAndSpeedInfusion(t *testing.T) {
	p := cast(t, 5101006, 20)
	if p.MPCost != 11 || p.Self.Duration != 200*time.Second || stats(p.Self)["BOOSTER"] != -2 {
		t.Fatalf("knuckle booster %+v %+v", p, p.Self)
	}
	p = cast(t, 5221000, 30)
	if p.Self != nil || p.Party == nil || p.Area == nil || p.Area.Left >= 0 || stats(p.Party)["MAPLE_WARRIOR"] != 15 || p.Party.Duration != 900*time.Second || p.MPCost != 70 {
		t.Fatalf("maple warrior %+v %+v", p, p.Party)
	}
	p = cast(t, 5121009, 20)
	if p.Party == nil || stats(p.Party)["SPEED_INFUSION"] != -2 || p.MPCost != 40 || p.HPCost != 40 {
		t.Fatalf("speed infusion %+v", p)
	}
	p = cast(t, 5321010, 30)
	if stats(p.Self)["STANCE"] != 15 || p.Self.Duration != 300*time.Second {
		t.Fatalf("pirate's spirit %+v", p.Self)
	}
}

func TestPiratesRevengeTriggersOnDamage(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	h, _ := skills.Lookup(5120011)
	if h.Cast != nil || h.OnDamaged == nil {
		t.Fatal("revenge is a damage trigger, not a cast")
	}
	def, v := values(t, 5120011, 10)
	now := time.Unix(1000, 0)
	ctx := func(job, damage int, rows []domain.SkillBuff, cds []domain.SkillCooldown) *skills.TriggerContext {
		return &skills.TriggerContext{Skill: def, Level: 10, Values: v, Character: domain.Character{Job: job, SkillCooldowns: cds}, Rows: rows, Damage: damage, Now: now}
	}
	rollDie = func(int) int { return 40 } // prop 40 at level 10
	c := h.OnDamaged(ctx(512, 100, nil, nil))
	if c == nil || c.Cooldown != 50*time.Second || c.Buff.Duration != 45*time.Second || c.Buff.Stats[0] != (skills.StatValue{Stat: "PIRATES_REVENGE", Value: 15}) {
		t.Fatalf("revenge %+v", c)
	}
	for name, x := range map[string]*skills.TriggerContext{
		"wrong job": ctx(522, 100, nil, nil),
		"no damage": ctx(512, 0, nil, nil),
		"active":    ctx(512, 100, []domain.SkillBuff{{ID: 5120011, Stat: "PIRATES_REVENGE", Value: 15, ExpiresAt: now.Add(time.Second)}}, nil),
		"cooling":   ctx(512, 100, nil, []domain.SkillCooldown{{ID: 5120011, ExpiresAt: now.Add(time.Second)}}),
	} {
		if c := h.OnDamaged(x); c != nil {
			t.Errorf("%s triggered", name)
		}
	}
	rollDie = func(int) int { return 41 }
	if c := h.OnDamaged(ctx(512, 100, nil, nil)); c != nil {
		t.Fatal("prop roll failed but triggered")
	}
}

func TestDashAndMorphs(t *testing.T) {
	p := cast(t, 5001005, 10)
	if s := stats(p.Self); s["DASH_SPEED"] != 30 || s["DASH_JUMP"] != 20 || p.Self.Duration != 20*time.Second || p.MPCost != 5 {
		t.Fatalf("dash %+v %+v", p, p.Self)
	}
	p = cast(t, 5111005, 20)
	if p.Self.Stats[0] != (skills.StatValue{Stat: "STANCE", Value: 100}) {
		t.Fatalf("transformation must lead with STANCE: %+v", p.Self.Stats)
	}
	if s := stats(p.Self); s["MORPH"] != 1000 || s["ENHANCED_WATK"] != 20 || s["ENHANCED_WDEF"] != 200 || s["ENHANCED_MDEF"] != 200 || s["SPEED"] != 40 || s["JUMP"] != 20 || p.Cooldown != 380*time.Second {
		t.Fatalf("transformation %+v", s)
	}
	p = cast(t, 5121003, 20)
	if s := stats(p.Self); s["MORPH"] != 1001 || s["STANCE"] != 100 || s["ENHANCED_WATK"] != 40 {
		t.Fatalf("super transformation %+v", s)
	}
	p = cast(t, 5101007, 10)
	if s := stats(p.Self); s["MORPH"] != 1002 || s["STANCE"] != 0 || len(s) != 1 || p.Cooldown != 10*time.Second {
		t.Fatalf("oak barrel %+v %+v", p, s)
	}
}

func TestMonkeyMagicIndieStats(t *testing.T) {
	p := cast(t, 5301003, 20)
	s := stats(p.Party)
	if s["HP_BOOST"] != 300 || s["MP_BOOST"] != 300 || s["ANGEL_ACC"] != 160 || s["ANGEL_AVOID"] != 160 || s["ANGEL_JUMP"] != 5 || s["ANGEL_SPEED"] != 10 || s["ANGEL_STAT"] != 20 || p.Party.Duration != 300*time.Second {
		t.Fatalf("monkey magic %+v", s)
	}
}

func TestDiceRolls(t *testing.T) {
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 1 }
	if p := cast(t, 5211007, 20); p.Self != nil || p.MPCost != 60 || p.Cooldown != 200*time.Second {
		t.Fatalf("a one has no buff but costs: %+v", p)
	}
	rollDie = func(int) int { return 4 }
	if p := cast(t, 5311004, 10); stats(p.Self)["DICE_ROLL"] != 4 || p.Self.Duration != 240*time.Second {
		t.Fatalf("barrel %+v", p.Self)
	}
	rolls := []int{3, 5, 3} // first die, prop roll, second die
	rollDie = func(int) int { v := rolls[0]; rolls = rolls[1:]; return v }
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 300 {
		t.Fatalf("double down %+v", p.Self)
	}
	rolls = []int{6, 100, 0}
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 6 {
		t.Fatalf("single die %+v", p.Self)
	}
	rolls = []int{1, 1, 4}
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 4 {
		t.Fatalf("second die only %+v", p.Self)
	}
	rolls = []int{2, 1, 5}
	if p := cast(t, 5320007, 5); stats(p.Self)["DICE_ROLL"] != 25 {
		t.Fatalf("two dice %+v", p.Self)
	}
}

func passive(t *testing.T, id, level int, c domain.Character) skills.Bonus {
	t.Helper()
	h, ok := skills.Lookup(id)
	if !ok || h.Passive == nil {
		t.Fatalf("%d has no passive", id)
	}
	def, v := values(t, id, level)
	return h.Passive(&skills.PassiveContext{Skill: def, Level: level, Values: v, Character: c})
}

func TestMasteryNeedsTheMatchingWeapon(t *testing.T) {
	withWeapon := func(job, item int) domain.Character {
		return domain.Character{Job: job, Equipment: []domain.Equipment{{ItemID: item, Slot: -11}}}
	}
	if b := passive(t, 5100001, 20, withWeapon(510, 1482000)); b.Mastery != 50 || b.ACC != 120 {
		t.Fatalf("knuckle mastery %+v", b)
	}
	if b := passive(t, 5100001, 20, withWeapon(510, 1492000)); b != (skills.Bonus{}) {
		t.Fatalf("knuckle mastery with a gun %+v", b)
	}
	if b := passive(t, 5300005, 20, withWeapon(530, 1532000)); b.Mastery != 50 || b.ACC != 120 {
		t.Fatalf("cannon mastery %+v", b)
	}
	if b := passive(t, 5010003, 10, withWeapon(531, 1532000)); b.WATK != 20 {
		t.Fatalf("cannon upgrade %+v", b)
	}
	if b := passive(t, 5010003, 10, withWeapon(510, 1482000)); b != (skills.Bonus{}) {
		t.Fatalf("cannon upgrade for a brawler %+v", b)
	}
	if b := passive(t, 5110000, 20, domain.Character{Job: 511}); b.Critical != 30 {
		t.Fatalf("critical punch %+v", b)
	}
	if b := passive(t, 5100009, 10, domain.Character{Job: 510}); b.MaxHPPercent != 20 {
		t.Fatalf("hp increase %+v", b)
	}
	if b := passive(t, 5000000, 20, domain.Character{Job: 500}); b.ACC != 160 || b.AVOID != 160 {
		t.Fatalf("bullet time %+v", b)
	}
}

func TestAttackSpecsCarryAmmoStatusAndDrain(t *testing.T) {
	spec := func(id int) *combat.AttackSpec {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil {
			t.Fatalf("%d is no attack", id)
		}
		return h.Attack
	}
	if s := spec(5001003); s.Kind != combat.Shoot || s.Ammo != combat.AmmoAuto {
		t.Fatalf("double shot %+v", s)
	}
	if s := spec(5321012); s.Kind != combat.Shoot || s.Ammo != combat.AmmoNone {
		t.Fatalf("cannon barrage %+v", s)
	}
	if s := spec(5201002); s.Kind != combat.Melee || s.Prepare == nil {
		t.Fatalf("grenade is a close-range charge attack with poison %+v", s)
	}
	if s := spec(5101002); len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobStun {
		t.Fatalf("backspin blow stuns %+v", s.OnHit)
	}
	if s := spec(5011002); len(s.OnHit) != 1 || s.OnHit[0].Stat != combat.MobSpeed || s.OnHit[0].ValueFrom != "z" {
		t.Fatalf("blast back slows by z %+v", s.OnHit)
	}

	def, _ := values(t, 5211005, 20)
	e, err := combat.FromSkill(def, 20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &combat.AttackContext{Effect: e}
	if err := spec(5211005).Prepare(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 1 || ctx.Effect.MonsterStatus[0].Stat != combat.MobFreeze || ctx.Effect.MonsterStatus[0].Duration != 8*time.Second {
		t.Fatalf("ice splitter freezes twice its time %+v", ctx.Effect.MonsterStatus)
	}
	def, _ = values(t, 5201002, 20)
	if e, err = combat.FromSkill(def, 20); err != nil {
		t.Fatal(err)
	}
	ctx = &combat.AttackContext{Effect: e}
	if err := spec(5201002).Prepare(ctx); err != nil || len(ctx.Effect.MonsterStatus) != 1 || !ctx.Effect.MonsterStatus[0].Poison || ctx.Effect.MonsterStatus[0].Duration != 8*time.Second {
		t.Fatalf("grenade poisons for dotTime %+v", ctx.Effect.MonsterStatus)
	}

	ctx = &combat.AttackContext{Effect: combat.Effect{X: 20}, Character: domain.Character{MaxHP: 1000}}
	if hp, mp := spec(5111004).Drain(ctx, 1000); hp != 200 || mp != 0 {
		t.Fatalf("energy drain %d %d", hp, mp)
	}
	if hp, _ := spec(5111004).Drain(ctx, 100000); hp != 500 {
		t.Fatalf("energy drain is capped at half MaxHP: %d", hp)
	}
}

func TestSummonsAndMindControl(t *testing.T) {
	for _, c := range []struct {
		id    int
		stat  buffstat.Stat
		move  uint8
		multi bool
	}{{5211001, "PUPPET", 0, true}, {5211002, "SUMMON", 5, true}, {5220002, "PUPPET", 0, true}, {5321003, "PUPPET", 0, false}, {5321004, "SUMMON", 1, false}} {
		h, _ := skills.Lookup(c.id)
		s := h.Attack.Summon
		if h.Attack.Kind != combat.Summoned || s == nil || s.MoveAbility != c.move || s.AssistType != 1 || s.Multi != c.multi || s.Puppet {
			t.Fatalf("%d summon %+v", c.id, s)
		}
		_, v := values(t, c.id, 10)
		if p := cast(t, c.id, 10); stats(p.Self)[c.stat] != 1 || p.Self.Duration != time.Duration(v["time"])*time.Second {
			t.Fatalf("%d summon buff %+v", c.id, p.Self)
		}
	}
	stats := combat.PlayerStats{Job: 532, Level: 120, STR: 400, DEX: 100, WATK: 100, WeaponID: 1532000}
	e := combat.Effect{SkillID: 5321003, Level: 15, Damage: 500}
	plain := combat.SummonCeiling(combat.DamageContext{Stats: stats, Effect: e})
	h, _ := skills.Lookup(5321003)
	if got := h.Attack.MaxDamage(combat.DamageContext{Stats: stats, Effect: e}); got <= plain {
		t.Fatalf("anchor ceiling %d must add selfDestruction to %d", got, plain)
	}
	h, _ = skills.Lookup(5221009)
	if h.Cast != nil || h.Attack.MobCast != nil || h.Attack.Kind != combat.Shoot || h.Attack.Ammo != combat.AmmoNone || len(h.Attack.OnHit) != 1 || h.Attack.OnHit[0].Stat != combat.MobDazzle {
		t.Fatalf("mind control is a ranged attack that hypnotizes %+v", h.Attack)
	}
}

func TestSpecialCasts(t *testing.T) {
	for _, id := range []int{5121008, 5721002, 5821008, 5921010} {
		if p := cast(t, id, 5); !p.Dispel || p.Self != nil || p.Party != nil || p.Cooldown != 360*time.Second || p.MPCost != 30 {
			t.Fatalf("maple purify %d %+v", id, p)
		}
	}
	if p := cast(t, 5201005, 10); p.Self != nil || p.Party != nil || p.MPCost != 14 {
		t.Fatalf("wings is cost only %+v", p)
	}
	if p := cast(t, 5821010, 30); !p.ResetCooldowns || p.Party == nil || len(p.Party.Stats) != 0 || p.Area == nil || p.Cooldown != 1200*time.Second {
		t.Fatalf("time leap %+v", p)
	}
	if p := cast(t, 5101005, 10); p.HPCost != 500 || p.Heal == nil || p.Heal.MP != 300 || p.Cooldown != 10*time.Second {
		t.Fatalf("mp recovery %+v %+v", p, p.Heal)
	}
	p := cast(t, 5221006, 10)
	if !p.Toggle || p.Self.Duration != skills.Permanent || p.Self.Stats[0] != (skills.StatValue{Stat: "STANCE", Value: 100}) {
		t.Fatalf("battleship %+v", p.Self)
	}
	if s := stats(p.Self); s["MONSTER_RIDING"] != battleshipMount || s["ENHANCED_WATK"] != 20 || s["ENHANCED_WDEF"] != 700 || s["ENHANCED_MAXHP"] != 600 || s["ENHANCED_MAXMP"] != 600 {
		t.Fatalf("battleship stats %+v", s)
	}
}

func TestPassiveDamageAndStatBonuses(t *testing.T) {
	c := domain.Character{Job: 512}
	if b := passive(t, 5110008, 10, c); b.SkillDamage[0] != (skills.SkillDamage{Skill: 5101002, Percent: 120}) || b.SkillDamage[2] != (skills.SkillDamage{Skill: 5101004, Percent: 150}) {
		t.Fatalf("melee mastery %+v", b.SkillDamage)
	}
	if b := passive(t, 5220001, 30, domain.Character{Job: 522}); b.SkillDamage[1] != (skills.SkillDamage{Skill: 5211005, Percent: 80}) {
		t.Fatalf("elemental boost %+v", b.SkillDamage)
	}
	if b := passive(t, 5300008, 10, domain.Character{Job: 530}); b.STR != 30 || b.DEX != 30 {
		t.Fatalf("monkey training %+v", b)
	}
	if b := passive(t, 5320009, 30, domain.Character{Job: 532}); b.DamagePercent != 40 || b.BossDamagePercent != 40 || b.IgnoreDefense != 20 {
		t.Fatalf("mega monkey cannon %+v", b)
	}
	if b := passive(t, 5310007, 20, domain.Character{Job: 531}); b.MaxHPPercent != 20 || b.ASR != 20 || b.WDEFPercent != 50 {
		t.Fatalf("ultimate state %+v", b)
	}
	if b := passive(t, 5300004, 15, domain.Character{Job: 530}); b.Critical != 20 || b.CriticalDamageMin != 10 {
		t.Fatalf("critical fire %+v", b)
	}
}

func TestEnergyChargeGauge(t *testing.T) {
	h, _ := skills.Lookup(5110001)
	def, v := values(t, 5110001, 20)
	now := time.Unix(1000, 0)
	attack := &combat.AttackEvent{Targets: []int32{1, 2, 3}, Lines: 2}
	run := func(job int, rows []domain.SkillBuff) *skills.BuffChange {
		return h.OnAttack(&skills.TriggerContext{Skill: def, Level: 20, Values: v, Character: domain.Character{Job: job}, Rows: rows, Attack: attack, Now: now})
	}
	row := func(value int, left time.Duration) []domain.SkillBuff {
		return []domain.SkillBuff{{ID: 5110001, Stat: "ENERGY_CHARGE", Value: value, ExpiresAt: now.Add(left)}}
	}
	if c := run(510, nil); c != nil {
		t.Fatal("brawlers have no energy")
	}
	if c := run(512, nil); c == nil || c.Buff.Duration != skills.Permanent || c.Buff.Stats[0].Value != 0 {
		t.Fatalf("first attack starts the gauge %+v", c)
	}
	if c := run(511, row(0, skills.Permanent)); c.Buff.Stats[0].Value != 1200 || c.Buff.Duration != skills.Permanent {
		t.Fatalf("x=200 per target line %+v", c.Buff)
	}
	if c := run(511, row(9900, skills.Permanent)); c.Buff.Stats[0].Value != energyFull {
		t.Fatalf("capped %+v", c.Buff)
	}
	if c := run(511, row(energyFull, skills.Permanent)); c.Buff.Stats[0].Value != energyFull || c.Buff.Duration != 60*time.Second {
		t.Fatalf("full charge is timed %+v", c.Buff)
	}
	if c := run(511, row(energyFull, 30*time.Second)); c != nil {
		t.Fatalf("timed charge is unchanged %+v", c)
	}
}

func TestHomingBeaconMarksTheFirstTarget(t *testing.T) {
	for _, id := range []int{5211006, 5220011} {
		h, _ := skills.Lookup(id)
		def, v := values(t, id, 10)
		ctx := &skills.TriggerContext{Skill: def, Level: 10, Values: v, Character: domain.Character{Job: 522}, Attack: &combat.AttackEvent{SkillID: id, Targets: []int32{77, 78}}}
		if c := h.OnAttack(ctx); c == nil || c.Buff.Duration != skills.Permanent || c.Buff.Stats[0] != (skills.StatValue{Stat: "HOMING_BEACON", Value: 77}) {
			t.Fatalf("%d %+v", id, c)
		}
		ctx.Attack.SkillID = 5001003
		if c := h.OnAttack(ctx); c != nil {
			t.Fatalf("%d reacts to another skill", id)
		}
	}
}

func TestGaviotaHitsUpToThree(t *testing.T) {
	h, _ := skills.Lookup(5211002)
	ctx := &combat.AttackContext{Effect: combat.Effect{MobCount: 1}}
	if h.Attack.Prepare == nil || h.Attack.Prepare(ctx) != nil || ctx.Effect.MobCount != 3 {
		t.Fatalf("gaviota mob count %d", ctx.Effect.MobCount)
	}
	if h, _ := skills.Lookup(5220002); h.Attack.Prepare != nil {
		t.Fatal("octopus keeps the level mob count")
	}
}

// The client follows Barrel Bomb (5301001) with the invisible blast 5300007,
// which no character learns: the follow-up is cast at Barrel Bomb's level so
// the world does not reject it as unlearned.
func TestBarrelBombFollowUpIsCastAtBarrelBombLevel(t *testing.T) {
	h, ok := skills.Lookup(5300007)
	if !ok || h.Attack == nil || h.LevelFrom != 5301001 || h.Attack.LevelFrom != 5301001 {
		t.Fatalf("5300007 must be linked to 5301001: %+v", h)
	}
	now := time.Unix(0, 0)
	c := domain.Character{Job: 530, Skills: []domain.SkillState{{ID: 5301001, Level: 20}}}
	if got := skills.LinkedLevel(c, h.LevelFrom, 5300007, now); got < 1 {
		t.Fatalf("follow-up level %d, want Barrel Bomb's", got)
	}
	if got := skills.LinkedLevel(domain.Character{Job: 530}, h.LevelFrom, 5300007, now); got != 0 {
		t.Fatalf("unlearned Barrel Bomb still casts the follow-up at %d", got)
	}
	def, err := content.SkillDefinition("", 5300007)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := combat.FromSkill(def, min(20, def.MaxLevel)); err != nil || e.Damage <= 0 || e.MobCount < 2 {
		t.Fatalf("follow-up effect %+v %v", e, err)
	}
}

// Barrel Bomb's barrel rolls x px (350) before bursting; the three hits and
// the hidden blast are admitted that far from the character.
func TestBarrelBombAdmitsTargetsWhereTheBarrelBursts(t *testing.T) {
	for _, id := range []int{5301001, 5300007} {
		h, ok := skills.Lookup(id)
		if !ok || h.Attack == nil || h.Attack.Prepare == nil {
			t.Fatalf("%d must widen its box", id)
		}
		def, _ := values(t, id, 20)
		e, err := combat.FromSkill(def, 20)
		if err != nil {
			t.Fatal(err)
		}
		before := e.Box
		ctx := &combat.AttackContext{Effect: e}
		if err := h.Attack.Prepare(ctx); err != nil {
			t.Fatal(err)
		}
		if ctx.Effect.Box.LT.X != before.LT.X-350 || ctx.Effect.Box.RB.X != before.RB.X+350 || ctx.Effect.Box.LT.Y != before.LT.Y {
			t.Fatalf("%d box %+v, want %+v widened by 350", id, ctx.Effect.Box, before)
		}
	}
}
