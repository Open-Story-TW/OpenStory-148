package combat

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func warrior() domain.Character {
	return domain.Character{ID: 9, Job: 100, Level: 30, HP: 500, MP: 100, STR: 100, DEX: 30,
		Equipment: []domain.Equipment{{Slot: -11, ItemID: 1302000, WATK: 40}, {Slot: -101, ItemID: 1702000, WATK: 999}}}
}

func TestLineCeilingMatchesReviewedWarriorProfile(t *testing.T) {
	c := warrior()
	c.Skills = []domain.SkillState{{ID: 1001004, Level: 5}}
	p, err := content.PhysicalAttackProfileFor("", c, 1001004, time.Now())
	if err != nil {
		t.Skip("seed has no 1001004:", err)
	}
	mob := content.Mob{PDRate: 10, HP: 100}
	want, _ := p.MaximumDamage(mob)
	s, _ := BaseStats(c, time.Now())
	got := LineCeiling(DamageContext{Stats: s, Effect: Effect{SkillID: 1001004, Damage: p.DamagePercent}, Mob: mob})
	if got != want {
		t.Fatalf("generic ceiling %d, reviewed profile %d", got, want)
	}
	if LineCeiling(DamageContext{Stats: s, Effect: Effect{SkillID: 1001004, Damage: 100}, Mob: content.Mob{OnlyNormalAttack: true}}) != 0 {
		t.Fatal("skill damage allowed on only-normal-attack mob")
	}
	if LineCeiling(DamageContext{Stats: s, Effect: Effect{Damage: 100}, Mob: content.Mob{}, Statuses: MobStatuses{MobPImmune: {Stat: MobPImmune}}}) != 1 {
		t.Fatal("physical immunity not applied")
	}
}

func TestElementAndWeaponFamilies(t *testing.T) {
	m := content.Mob{ElemAttr: "F1I3"}
	if MobEffectiveness(m, Fire) != Immune || MobEffectiveness(m, Ice) != Weak || MobEffectiveness(m, Lightning) != Normal {
		t.Fatal("elemAttr parse")
	}
	mage := PlayerStats{Job: 200, INT: 100, LUK: 20, MATK: 50, WeaponID: 1372000}
	if MaxBaseDamage(mage) != (4*100+20)*50/100 {
		t.Fatalf("mage base %d", MaxBaseDamage(mage))
	}
	thief := PlayerStats{Job: 400, LUK: 100, DEX: 20, STR: 4, WATK: 20, WeaponID: 1472000}
	// Claw secondary stat is DEX alone (client CalcDamage weapon type 47).
	if MaxBaseDamage(thief) != 175*(4*100+20)*20/10000 || WeaponFor(1472000).Projectile != AmmoStar {
		t.Fatal("claw base/ammo")
	}
	weak := LineCeiling(DamageContext{Stats: mage, Effect: Effect{SkillID: 2201001, Damage: 100, Element: Ice}, Mob: m, Magic: true})
	normal := LineCeiling(DamageContext{Stats: mage, Effect: Effect{SkillID: 2201001, Damage: 100, Element: Lightning}, Mob: m, Magic: true})
	if weak <= normal || LineCeiling(DamageContext{Stats: mage, Effect: Effect{SkillID: 2101004, Damage: 100, Element: Fire}, Mob: m, Magic: true}) != 1 {
		t.Fatalf("element multipliers weak%d normal%d", weak, normal)
	}
}

func TestBoxMirrorsForFacingRight(t *testing.T) {
	b := Box{LT: Point{-200, -50}, RB: Point{-10, 10}, Set: true}
	lt, rb := b.At(Point{100, 0}, true)
	if lt != (Point{-100, -50}) || rb != (Point{90, 10}) {
		t.Fatalf("left %v %v", lt, rb)
	}
	lt, rb = b.At(Point{100, 0}, false)
	if lt != (Point{110, -50}) || rb != (Point{300, 10}) {
		t.Fatalf("right %v %v", lt, rb)
	}
}

func attack(op uint16, skill int, display uint16, x int16, targets map[int32][]uint32) protocol.Attack {
	a := protocol.Attack{Opcode: op, SkillID: skill, Display: display, X: x}
	hits := 0
	for id, lines := range targets {
		t := protocol.AttackTarget{ObjectID: id}
		for _, l := range lines {
			t.Lines = append(t.Lines, protocol.AttackLine{Raw: l, Damage: int(l & 0x7fffffff)})
		}
		hits = len(lines)
		a.Targets = append(a.Targets, t)
	}
	a.TByte = byte(len(a.Targets)<<4 | hits)
	return a
}

func TestPlanAttackCountsRangeCeilingCostAndStatus(t *testing.T) {
	c := warrior()
	s, _ := BaseStats(c, time.Now())
	now := time.Unix(100, 0)
	e := Effect{SkillID: 1001005, Level: 1, Damage: 100, MobCount: 2, AttackCount: 1, MPCon: 8, Prop: 100, Time: 3 * time.Second,
		Box:           Box{LT: Point{-150, -60}, RB: Point{0, 10}, Set: true},
		MonsterStatus: []MobStatusApply{{Stat: MobStun}}}
	mob := content.Mob{HP: 1000}
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: e, Now: now,
		Attack:  attack(protocol.RecvMeleeAttack, 1001005, 0x0020, 0, map[int32][]uint32{5: {999999}}),
		Targets: []TargetInfo{{ObjectID: 5, Mob: mob, X: -100, Y: 0, HP: 1000}}}
	p, err := PlanAttack(nil, ctx, nil)
	if err != nil || p.Cost.MP != 8 || len(p.Targets) != 1 || p.Targets[0].Lines[0] != CritCeiling(DamageContext{Stats: s, Effect: e, Mob: mob}, LineCeiling(DamageContext{Stats: s, Effect: e, Mob: mob})) || len(p.Targets[0].Statuses) != 1 || p.Targets[0].Statuses[0].ExpiresAt != now.Add(3*time.Second) {
		t.Fatalf("plan %+v %v", p, err)
	}
	ctx.Attack.Display |= 0x8000 // Facing left: either mirrored box remains tolerated.
	if _, err = PlanAttack(nil, ctx, nil); err != nil {
		t.Fatalf("mirrored facing rejected %v", err)
	}
	ctx.Attack.Display &^= 0x8000
	ctx.Targets[0].X = 500 // Outside both mirrors.
	if _, err = PlanAttack(nil, ctx, nil); !errors.Is(err, ErrRange) {
		t.Fatalf("out of range admitted %v", err)
	}
	ctx.Targets[0].X = -100
	ctx.Character.MP = 7
	if _, err = PlanAttack(nil, ctx, nil); err != ErrCost {
		t.Fatal("unpayable MP accepted")
	}
	ctx.Character.MP = 100
	ctx.Attack.TByte = 0x12
	if _, err = PlanAttack(nil, ctx, nil); !errors.Is(err, ErrCount) {
		t.Fatal("extra lines accepted")
	}
	boss := ctx
	boss.Attack.TByte = 0x11
	boss.Targets = []TargetInfo{{ObjectID: 5, Mob: content.Mob{HP: 1000, Boss: true}, X: -100, HP: 1000}}
	if p, _ = PlanAttack(nil, boss, nil); len(p.Targets[0].Statuses) != 0 {
		t.Fatal("boss stunned")
	}
	if _, err = PlanAttack(&AttackSpec{Kind: Magic}, ctx, nil); err != ErrKind {
		t.Fatal("magic-only skill accepted melee input")
	}
}

func TestShootAmmoCost(t *testing.T) {
	c := domain.Character{ID: 1, Job: 300, Level: 20, HP: 100, MP: 50, DEX: 60, STR: 4,
		Equipment: []domain.Equipment{{Slot: -11, ItemID: 1452002, WATK: 30}}}
	s, _ := BaseStats(c, time.Now())
	ctx := AttackContext{Kind: Shoot, Character: c, Stats: s, Effect: Effect{SkillID: 3001005, Damage: 150, MobCount: 1, AttackCount: 2, BulletCount: 2},
		Attack: attack(protocol.RecvShootAttack, 3001005, 0, 0, map[int32][]uint32{3: {10, 10}}), Targets: []TargetInfo{{ObjectID: 3, Mob: content.Mob{HP: 50}, X: 300, HP: 50}}}
	p, err := PlanAttack(nil, ctx, nil)
	if err != nil || p.Cost.Ammo != AmmoArrow || p.Cost.Bullets != 2 {
		t.Fatalf("ammo %+v %v", p.Cost, err)
	}
	if p, _ = PlanAttack(&AttackSpec{Kind: Shoot, Ammo: AmmoNone}, ctx, nil); p.Cost.Bullets != 0 {
		t.Fatal("soul-arrow style skill consumed ammo")
	}
}

func TestMobStatusPoisonTickNeverKillsAndExpires(t *testing.T) {
	now := time.Unix(0, 0)
	e := Effect{SkillID: 2101005, Time: 4 * time.Second, DOT: 20, DOTTime: 4 * time.Second, DOTInterval: time.Second}
	st, ok := Resolve(MobStatusApply{Stat: MobPoison, Poison: true}, e, 7, 500, now, nil)
	if !ok || st.TickDamage != 100 || st.ExpiresAt != now.Add(4*time.Second) {
		t.Fatalf("poison %+v", st)
	}
	m := MobStatuses{}
	if !m.Apply(st) || m.Apply(st) {
		t.Fatal("same-source poison stacked")
	}
	hp, hits := m.Tick(now.Add(2500*time.Millisecond), 250)
	if hp != 50 || len(hits) != 2 || hits[0].OwnerID != 7 {
		t.Fatalf("ticks hp%d %+v", hp, hits)
	}
	hp, _ = m.Tick(now.Add(4*time.Second), hp)
	if hp != 1 {
		t.Fatalf("poison reduced below 1: %d", hp)
	}
	if ended := m.Expire(now.Add(4 * time.Second)); len(ended) != 1 || len(m) != 0 {
		t.Fatal("expire")
	}
	if _, ok = Resolve(MobStatusApply{Stat: MobStun, Prop: 30}, e, 7, 1, now, func(int) bool { return false }); ok {
		t.Fatal("failed prop roll applied")
	}
	if CanInflict(content.Mob{ElemAttr: "S1"}, MobPoison, Poison) {
		t.Fatal("poison-immune mob poisoned")
	}
}

func TestClipToHP(t *testing.T) {
	l := []int{40, 40, 40}
	if ClipToHP(l, 90) != 90 || l[2] != 10 {
		t.Fatalf("%v", l)
	}
}

func TestPrepareHPRatioItemConsumeAndDrain(t *testing.T) {
	c := warrior()
	c.HP, c.MaxHP = 400, 500
	s, _ := BaseStats(c, time.Now())
	spec := &AttackSpec{Kind: Melee, IgnoreRange: true,
		Prepare: func(ctx *AttackContext) error {
			ctx.Effect.HPRatio = ctx.Effect.X
			ctx.Effect.Element = Holy
			return nil
		},
		Drain: func(_ *AttackContext, dealt int64) (int, int) { return int(dealt), 999 }}
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: Effect{SkillID: 1311005, Damage: 100, MobCount: 1, AttackCount: 1, X: 20, ItemCon: 2331000, ItemConNo: 2},
		Attack: attack(protocol.RecvMeleeAttack, 1311005, 0, 0, map[int32][]uint32{1: {50}}), Targets: []TargetInfo{{ObjectID: 1, Mob: content.Mob{HP: 30}, HP: 30}}}
	p, err := PlanAttack(spec, ctx, nil)
	if err != nil || p.Cost.HP != 80 || p.Cost.ItemID != 2331000 || p.Cost.Items != 2 || p.Heal != 30 || p.MPGain != 0 {
		t.Fatalf("plan %+v %v", p, err)
	}
	reject := &AttackSpec{Kind: Melee, Prepare: func(*AttackContext) error { return ErrRejected }}
	if _, err = PlanAttack(reject, ctx, nil); err != ErrRejected {
		t.Fatal("prepare rejection ignored")
	}
}

func TestAttackObservers(t *testing.T) {
	var got AttackEvent
	ObserveAttacks(func(e AttackEvent) { got = e })
	PublishAttack(AttackEvent{SkillID: 7, Damage: 3})
	if got.SkillID != 7 || got.Damage != 3 {
		t.Fatal("observer not called")
	}
}

func TestSubTimeAndCastOnlySpecRejectsAttacks(t *testing.T) {
	now := time.Unix(0, 0)
	st, ok := Resolve(MobStatusApply{Stat: MobDarkness, SubTime: true}, Effect{Time: 60 * time.Second, SubTime: 20 * time.Second}, 1, 1, now, nil)
	if !ok || st.ExpiresAt != now.Add(20*time.Second) {
		t.Fatalf("subTime %+v", st)
	}
	cast := &AttackSpec{MobCast: &MobCastSpec{}}
	if cast.Allows(Melee) || cast.Allows(Magic) || !(*AttackSpec)(nil).Allows(Melee) {
		t.Fatal("Kind 0 cast-only spec accepted an attack packet")
	}
}

func TestTickSourcesAndMonsterBombExplosion(t *testing.T) {
	now := time.Unix(0, 0)
	e := Effect{SkillID: 4111003, Time: 4 * time.Second, Damage: 300}
	web, _ := ResolveOn(MobStatusApply{Stat: MobWeb, Poison: true, TickFrom: "mobhp50"}, e, 1, 100, content.Mob{HP: 1000}, now, nil)
	amb, _ := ResolveOn(MobStatusApply{Stat: MobAmbush, Poison: true, TickFrom: "damage"}, e, 1, 100, content.Mob{}, now, nil)
	bomb, _ := ResolveOn(MobStatusApply{Stat: MobTimeBomb, Explode: true, TickFrom: "damage"}, e, 1, 100, content.Mob{}, now, nil)
	if web.TickDamage != 20 || amb.TickDamage != 300 || !bomb.Explode || bomb.TickDamage != 300 {
		t.Fatalf("tick sources web%d amb%d bomb%+v", web.TickDamage, amb.TickDamage, bomb)
	}
	m := MobStatuses{}
	m.Apply(bomb)
	if hp, hits := m.Tick(now.Add(3*time.Second), 1000); hp != 1000 || len(hits) != 0 {
		t.Fatal("bomb ticked before expiry")
	}
	if hp, hits := m.Tick(now.Add(4*time.Second), 1000); hp != 700 || len(hits) != 1 {
		t.Fatalf("bomb explosion hp%d", hp)
	}
	if hp, _ := m.Tick(now.Add(5*time.Second), 700); hp != 700 {
		t.Fatal("bomb exploded twice")
	}
	SetNormalAttack(&AttackSpec{Kind: Melee | Shoot, Prepare: func(c *AttackContext) error { c.NoAmmo = true; return nil }})
	defer SetNormalAttack(nil)
	if NormalAttack() == nil || !NormalAttack().Allows(Shoot) {
		t.Fatal("normal attack spec")
	}
}

func TestShadowPartnerLinesUseItsPercent(t *testing.T) {
	c := warrior()
	s, _ := BaseStats(c, time.Now())
	s.ShadowPartner = 50
	e := Effect{SkillID: 4001344, Damage: 100, MobCount: 1, AttackCount: 2}
	mob := content.Mob{HP: 1 << 30}
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: e,
		Attack: attack(protocol.RecvMeleeAttack, 4001344, 0, 0, map[int32][]uint32{1: {999999, 999999, 999999, 999999}}), Targets: []TargetInfo{{ObjectID: 1, Mob: mob, HP: 1 << 30}}}
	p, err := PlanAttack(&AttackSpec{Kind: Melee, IgnoreRange: true}, ctx, nil)
	full := CritCeiling(DamageContext{Stats: s, Effect: e, Mob: mob}, LineCeiling(DamageContext{Stats: s, Effect: e, Mob: mob}))
	if err != nil || p.Targets[0].Lines[0] != full || p.Targets[0].Lines[2] != full/2 {
		t.Fatalf("shadow partner lines %v full%d %v", p.Targets[0].Lines, full, err)
	}
}

func TestGlobalModifiersSeeAttackerAndTargetHP(t *testing.T) {
	c := warrior()
	c.Skills = append(c.Skills, domain.SkillState{ID: 3110001, Level: 1})
	c.HP, c.MaxHP = 100, 500
	s, _ := BaseStats(c, time.Now())
	ModifyCeiling(func(dc DamageContext, ceil int) int {
		for _, sk := range dc.Attacker.Skills {
			if sk.ID == 3110001 && dc.HP*100 < dc.Mob.HP*20 {
				return int(dc.HP) // Mortal Blow: low-HP targets die.
			}
		}
		return ceil
	})
	ModifyDrain(func(ctx *AttackContext, dealt int64) (int, int) { return 7, 0 })
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: Effect{SkillID: 3101005, Damage: 1, MobCount: 1, AttackCount: 1},
		Attack: attack(protocol.RecvMeleeAttack, 3101005, 0, 0, map[int32][]uint32{1: {999999}}), Targets: []TargetInfo{{ObjectID: 1, Mob: content.Mob{HP: 100000}, HP: 900}}}
	p, err := PlanAttack(&AttackSpec{Kind: Melee, IgnoreRange: true}, ctx, nil)
	if err != nil || p.Targets[0].Lines[0] != 900 || p.Heal != 7 {
		t.Fatalf("modifiers %+v %v", p, err)
	}
}

func TestFlagStatusDefaultsToValueOne(t *testing.T) {
	st, ok := Resolve(MobStatusApply{Stat: MobStun}, Effect{Time: 3 * time.Second}, 1, 1, time.Unix(0, 0), nil)
	if !ok || st.Value != 1 {
		t.Fatalf("stun value %d", st.Value)
	}
}

func TestSummonStatusesLastFourSeconds(t *testing.T) {
	c := warrior()
	s, _ := BaseStats(c, time.Now())
	now := time.Unix(10, 0)
	ctx := AttackContext{Kind: Summoned, Character: c, Stats: s, Now: now, Effect: Effect{SkillID: 3101007, Damage: 100, MobCount: 1, AttackCount: 1, Time: 150 * time.Second},
		Attack: attack(protocol.RecvSummonAttack, 3101007, 0, 0, map[int32][]uint32{1: {5}}), Targets: []TargetInfo{{ObjectID: 1, Mob: content.Mob{HP: 100}, HP: 100}}}
	p, err := PlanAttack(&AttackSpec{Kind: Summoned, Summon: &SummonSpec{}, IgnoreRange: true, OnHit: []MobStatusApply{{Stat: MobStun}}}, ctx, nil)
	if err != nil || len(p.Targets[0].Statuses) != 1 || p.Targets[0].Statuses[0].ExpiresAt != now.Add(4*time.Second) {
		t.Fatalf("summon stun %+v %v", p.Targets, err)
	}
}

func TestWideSkillsAreNotRejected(t *testing.T) {
	s := content.Skill{ID: 1009, MaxLevel: 1, Common: map[string]string{"mobCount": "30", "damage": "100"}}
	e, err := FromSkill(s, 1)
	if err != nil || e.MobCount != 30 {
		t.Fatalf("mobCount 30: %+v %v", e, err)
	}
}

func TestChargeElementFromBuffSource(t *testing.T) {
	now := time.Unix(100, 0)
	c := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 15101006, Stat: "WK_CHARGE", ExpiresAt: now.Add(time.Minute)}}}
	if e, ok := ChargeElement(c, now); !ok || e != Lightning {
		t.Fatalf("thunder charge %v %v", e, ok)
	}
	if _, ok := ChargeElement(c, now.Add(2*time.Minute)); ok {
		t.Fatal("expired charge")
	}
}

// Summon skills keep their target count in summon/attack1/info (Gaviota).
func TestFromSkillSummonMobCount(t *testing.T) {
	s, err := content.SkillDefinition("", 5211002)
	if err != nil {
		t.Fatal(err)
	}
	e, err := FromSkill(s, 1)
	if err != nil || e.MobCount != 3 || e.AttackCount != 1 {
		t.Fatalf("gaviota %+v %v", e, err)
	}
}

// FromSkill carries the WZ lt/rb box (Smoke Screen, mists, MobCast UseBox).
func TestFromSkillBox(t *testing.T) {
	s, err := content.SkillDefinition("", 4221006)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := s.RangeAt(1)
	e, err := FromSkill(s, 1)
	if err != nil || !ok || !e.Box.Set || e.Box.LT != (Point{r.LT[0], r.LT[1]}) || e.Box.RB != (Point{r.RB[0], r.RB[1]}) {
		t.Fatalf("smoke screen box %+v %+v %v", e.Box, r, err)
	}
}

// A Dual Blade katara in the shield slot makes the ordinary attack two hits.
func TestNormalEffectForKatara(t *testing.T) {
	if e := NormalEffectFor(domain.Character{}); e.AttackCount != 1 {
		t.Fatal("plain normal attack", e.AttackCount)
	}
	c := domain.Character{Equipment: []domain.Equipment{{Slot: -11, ItemID: 1332000}, {Slot: -10, ItemID: 1342000}}}
	if e := NormalEffectFor(c); e.AttackCount != 2 {
		t.Fatal("katara normal attack", e.AttackCount)
	}
}

// A magic spec's body attack (Teleport Mastery 0x4E) keeps the magic ceiling.
func TestBodyAttackOfMagicSpecUsesMagicCeiling(t *testing.T) {
	spec := &AttackSpec{Kind: Magic | Body, IgnoreRange: true}
	c := domain.Character{ID: 1, Level: 70, MP: 100, MaxMP: 100, HP: 100, MaxHP: 100}
	s := PlayerStats{MATK: 500, INT: 400, LUK: 50}
	e := Effect{SkillID: 2111007, Level: 1, Damage: 100, MobCount: 1, AttackCount: 1}
	mob := content.Mob{HP: 100000}
	ctx := AttackContext{Kind: Body, Character: c, Stats: s, Effect: e, Now: time.Now(),
		Attack:  attack(protocol.RecvBodyAttack, 2111007, 0, 0, map[int32][]uint32{5: {999999}}),
		Targets: []TargetInfo{{ObjectID: 5, Mob: mob, HP: 100000}}}
	p, err := PlanAttack(spec, ctx, nil)
	want := applyCeilingModifiers(DamageContext{Stats: s, Effect: e, Mob: mob, Magic: true, HP: 100000, Attacker: c}, LineCeiling(DamageContext{Stats: s, Effect: e, Mob: mob, Magic: true, HP: 100000, Attacker: c}))
	if err != nil || p.Targets[0].Lines[0] != want {
		t.Fatalf("body magic ceiling %v want %d: %v", p.Targets, want, err)
	}
}
