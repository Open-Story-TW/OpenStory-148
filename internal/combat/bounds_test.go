package combat

import (
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

// Client CalcDamage (Open-Maple docs/server/combat/client-damage-formula.md §2.2): weapon
// multipliers, cannon stat pick, bare hands scored without PAD.
func TestClientWeaponFamilies(t *testing.T) {
	bow := PlayerStats{Job: 312, DEX: 400, STR: 50, WATK: 100, WeaponID: 1452002}
	if got := MaxBaseDamage(bow); got != 130*(4*400+50)*100/10000 {
		t.Fatalf("bow base %d", got)
	}
	cannon := PlayerStats{Job: 532, DEX: 300, STR: 100, WATK: 100, WeaponID: 1532000}
	if got := MaxBaseDamage(cannon); got != 150*(4*300+100)*100/10000 {
		t.Fatalf("cannon base %d (DEX main)", got)
	}
	twoHanded := PlayerStats{Job: 112, STR: 400, DEX: 50, WATK: 100, WeaponID: 1402002}
	if got := MaxBaseDamage(twoHanded); got != 134*(4*400+50)*100/10000 {
		t.Fatalf("two-handed sword base %d", got)
	}
	bare := PlayerStats{Job: 112, STR: 400, DEX: 50, WATK: 100}
	if got := MaxBaseDamage(bare); got != 143*(4*400+50)/10000 {
		t.Fatalf("bare hands base %d must not use PAD", got)
	}
	beginner := PlayerStats{Job: 0, STR: 40, DEX: 20, LUK: 4, WATK: 20, WeaponID: 1472000}
	if got := MaxBaseDamage(beginner); got != 120*(4*40+20)*20/10000 {
		t.Fatalf("beginner with a claw base %d", got)
	}
}

func TestLineBoundsFloorIgnoreAndCrit(t *testing.T) {
	s := PlayerStats{Job: 312, Level: 120, DEX: 400, STR: 50, WATK: 100, WeaponID: 1452002, Mastery: 60}
	mob := content.Mob{HP: 100000, Level: 120, PDRate: 50}
	e := Effect{SkillID: 3121004, Damage: 300}
	floor, ceiling := LineBounds(DamageContext{Stats: s, Effect: e, Mob: mob})
	base := 130 * (4*400 + 50) * 100 // ×10000
	want := base * 300 * 50 / 100    // skill% then PDR 50%
	if ceiling != (want+999999)/1000000 {
		t.Fatalf("ceiling %d want %d", ceiling, (want+999999)/1000000)
	}
	// Mastery floor: bow constant 0.15 + 60% = 75% of the maximum.
	if floor != want*75/100/1000000 {
		t.Fatalf("floor %d want %d", floor, want*75/100/1000000)
	}
	// Ignored defense is a share of PDR: 50% × (1 − 40%) = 30%, not 10%.
	s.IgnoreDefense = 40
	floorIgnored, ignored := LineBounds(DamageContext{Stats: s, Effect: e, Mob: mob})
	if ignored != (base*300*70/100+999999)/1000000 {
		t.Fatalf("ignore defense ceiling %d", ignored)
	}
	// Level penalty lowers the floor only: 10 levels under → −10%.
	higher := mob
	higher.Level = 130
	lowFloor, sameCeiling := LineBounds(DamageContext{Stats: s, Effect: e, Mob: higher})
	if sameCeiling != ignored || lowFloor != floorIgnored*90/100 {
		t.Fatalf("level penalty floor %d ceiling %d", lowFloor, sameCeiling)
	}
	// A critical line may reach +50% (+criticaldamageMax); its floor is +20%.
	s.CritDamage = 25
	dc := DamageContext{Stats: s, Effect: e, Mob: mob}
	if CritCeiling(dc, 1000) != 1750 || CritFloor(1000) != 1200 || CritCeiling(dc, 1) != 1 {
		t.Fatalf("crit bounds %d %d", CritCeiling(dc, 1000), CritFloor(1000))
	}
}

func TestMagicAmplificationAndWandSwing(t *testing.T) {
	mage := PlayerStats{Job: 212, Level: 120, INT: 500, LUK: 100, MATK: 300, WeaponID: 1382000, ElementAmp: 50}
	mob := content.Mob{HP: 100000, Level: 100}
	plain := LineCeiling(DamageContext{Stats: mage, Effect: Effect{SkillID: 2121007, Damage: 200}, Mob: mob, Magic: true})
	fire := LineCeiling(DamageContext{Stats: mage, Effect: Effect{SkillID: 2121007, Damage: 200, Element: Fire}, Mob: mob, Magic: true})
	if plain != fire || plain != (100*(4*500+100)*300*200*150/100+999999)/1000000 {
		t.Fatalf("amplification plain %d fire %d", plain, fire)
	}
	// A mage swinging the staff: magic formula × 0.2.
	swing := LineCeiling(DamageContext{Stats: mage, Effect: Effect{Damage: 100}, Mob: mob})
	if swing != (100*(4*500+100)*300*100/5+999999)/1000000 {
		t.Fatalf("staff swing ceiling %d", swing)
	}
}

func TestV148FinalCutMultipliesOtherPhysicalAttacks(t *testing.T) {
	gun := DamageContext{
		Stats:  PlayerStats{Job: 522, Level: 200, STR: 100, DEX: 100, WATK: 41, WeaponID: 1492004},
		Effect: Effect{SkillID: 5221004, Damage: 330},
		Mob:    content.Mob{HP: 1000000, Level: 10},
	}
	if got := LineCeiling(gun); got != 1015 {
		t.Fatalf("unbuffed Rapid Fire ceiling = %d, want 1015", got)
	}
	gun.Stats.FinalCutPercent = 160
	if got := LineCeiling(gun); got != 1624 {
		t.Fatalf("Final Cut Rapid Fire ceiling = %d, want 1624", got)
	}
	gun.Magic = true
	if got := LineCeiling(gun); got != 1015 {
		t.Fatalf("Final Cut changed magic ceiling to %d", got)
	}
	gun.Magic = false
	gun.Effect.SkillID = 4341002
	if got := LineCeiling(gun); got != 1015 {
		t.Fatalf("Final Cut multiplied its own attack ceiling to %d", got)
	}
}

func TestV148BerserkFuryMultipliesPhysicalAndMagicAttacks(t *testing.T) {
	phys := DamageContext{
		Stats:  PlayerStats{Job: 522, Level: 200, STR: 100, DEX: 100, WATK: 41, WeaponID: 1492004, DamageRate: 20},
		Effect: Effect{SkillID: 5221004, Damage: 330},
		Mob:    content.Mob{HP: 1000000, Level: 10},
	}
	basePhys := LineCeiling(phys)
	phys.Stats.BerserkFuryPercent = 200
	if got := LineCeiling(phys); got != basePhys*2 {
		t.Fatalf("Berserk Fury physical ceiling = %d, want %d", got, basePhys*2)
	}
	magic := DamageContext{
		Stats:  PlayerStats{Job: 212, Level: 200, INT: 500, LUK: 100, MATK: 300, WeaponID: 1382000, DamageRate: 20},
		Effect: Effect{SkillID: 2121007, Damage: 200},
		Mob:    content.Mob{HP: 1000000, Level: 10},
		Magic:  true,
	}
	baseMagic := LineCeiling(magic)
	magic.Stats.BerserkFuryPercent = 200
	if got := LineCeiling(magic); got != baseMagic*2 {
		t.Fatalf("Berserk Fury magic ceiling = %d, want %d", got, baseMagic*2)
	}
}

func TestProjectileReachesRangePlusBlast(t *testing.T) {
	c := warrior()
	c.Job = 300
	s := PlayerStats{Job: 300, Level: 120, DEX: 400, STR: 50, WATK: 100, WeaponID: 1452002}
	e := Effect{SkillID: 3001004, Level: 20, Damage: 300, MobCount: 3, AttackCount: 1, Range: 420, Box: Box{LT: Point{-120, -75}, RB: Point{120, 30}, Set: true}}
	mob := content.Mob{HP: 1000000, Level: 120}
	plan := func(kind AttackKind, op uint16, x int) error {
		ctx := AttackContext{Kind: kind, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0), NoAmmo: true,
			Attack:  attack(op, 3001004, 0x0010, 0, map[int32][]uint32{5: {100}}),
			Targets: []TargetInfo{{ObjectID: 5, Mob: mob, X: x, Y: 0, HP: 1000000}}}
		_, err := PlanAttack(nil, ctx, nil)
		return err
	}
	if err := plan(Shoot, protocol.RecvShootAttack, 195); err != nil {
		t.Fatalf("live Arrow Blow distance rejected: %v", err)
	}
	if err := plan(Shoot, protocol.RecvShootAttack, (420+120)*rangeSlack+1); !errors.Is(err, ErrRange) {
		t.Fatalf("past range and blast admitted: %v", err)
	}
	if err := plan(Melee, protocol.RecvMeleeAttack, 120*rangeSlack+1); !errors.Is(err, ErrRange) {
		t.Fatalf("melee keeps its box: %v", err)
	}
}

func TestPlanAttackUsesCriticalFlagAndCountsFloorViolations(t *testing.T) {
	c := warrior()
	c.Job = 312
	s := PlayerStats{Job: 312, Level: 120, DEX: 400, STR: 50, WATK: 100, WeaponID: 1452002, Mastery: 60}
	e := Effect{SkillID: 3121004, Level: 30, Damage: 300, MobCount: 1, AttackCount: 1, Box: Box{LT: Point{-400, -100}, RB: Point{0, 100}, Set: true}}
	mob := content.Mob{HP: 1000000, Level: 120}
	floor, ceiling := LineBounds(DamageContext{Stats: s, Effect: e, Mob: mob})
	plan := func(raw uint32) TargetPlan {
		ctx := AttackContext{Kind: Shoot, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0), NoAmmo: true,
			Attack:  attack(protocol.RecvShootAttack, 3121004, 0x0010, 0, map[int32][]uint32{5: {raw}}),
			Targets: []TargetInfo{{ObjectID: 5, Mob: mob, X: -100, Y: 0, HP: 1000000}}}
		p, err := PlanAttack(nil, ctx, nil)
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		return p.Targets[0]
	}
	if tp := plan(uint32(ceiling)); tp.Lines[0] != ceiling || tp.Clipped != 0 || tp.BelowFloor != 0 {
		t.Fatalf("max non-crit line %+v", tp)
	}
	crit := CritCeiling(DamageContext{Stats: s, Effect: e, Mob: mob}, ceiling)
	// The client sends no critical flag: a value only a critical reaches is kept and marked.
	if tp := plan(uint32(crit)); tp.Lines[0] != crit || tp.Clipped != 0 || !tp.Critical[0] {
		t.Fatalf("unflagged critical value: %+v", tp)
	}
	if tp := plan(uint32(crit + 1)); tp.Lines[0] != crit || tp.Clipped != 1 {
		t.Fatalf("above the critical bound must clip: %+v", tp)
	}
	if tp := plan(uint32(ceiling)); tp.Critical[0] {
		t.Fatalf("a value a normal hit reaches is not provably critical: %+v", tp)
	}
	if tp := plan(0x80000000 | uint32(crit)); tp.Lines[0] != crit || tp.Clipped != 0 || tp.BelowFloor != 0 {
		t.Fatalf("flagged crit line %+v", tp)
	}
	if tp := plan(uint32(floor - 1)); tp.Lines[0] != floor-1 || tp.BelowFloor != 1 {
		t.Fatalf("under-floor line is kept but counted: %+v", tp)
	}
	if tp := plan(0); tp.Lines[0] != 0 || tp.BelowFloor != 0 {
		t.Fatalf("a miss (0) is never a floor violation: %+v", tp)
	}
}
