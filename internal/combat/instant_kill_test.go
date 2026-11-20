package combat

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

// Aerial Barrage's first line is the target's maximum HP when the native
// instant-kill roll succeeds (v148 0x71160B); it is above the display cap.
func TestAerialBarrageInstantKillFirstLine(t *testing.T) {
	c := warrior()
	s, _ := BaseStats(c, time.Now())
	e := Effect{SkillID: 23110006, Level: 1, Damage: 100, MobCount: 8, AttackCount: 2, Prop: 15,
		Box: Box{LT: Point{-150, -60}, RB: Point{0, 10}, Set: true}}
	mob := content.Mob{HP: 1_000_000}
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0),
		Attack:  attack(protocol.RecvMeleeAttack, 23110006, 0x0020, 0, map[int32][]uint32{5: {1_000_000, 1_000_000}}),
		Targets: []TargetInfo{{ObjectID: 5, Mob: mob, X: -100, HP: 1_000_000}}}
	p, err := PlanAttack(nil, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := p.Targets[0].Lines
	if lines[0] != 1_000_000 || lines[1] >= 1_000_000 || p.Targets[0].Clipped != 1 {
		t.Fatalf("instant kill must cover only the first line: %+v", p.Targets[0])
	}

	boss := ctx
	boss.Targets = []TargetInfo{{ObjectID: 5, Mob: content.Mob{HP: 1_000_000, Boss: true}, X: -100, HP: 1_000_000}}
	if p, _ = PlanAttack(nil, boss, nil); p.Targets[0].Lines[0] >= 1_000_000 {
		t.Fatalf("boss instant kill accepted: %+v", p.Targets[0])
	}
	other := ctx
	other.Effect.SkillID = 23101001
	if p, _ = PlanAttack(nil, other, nil); p.Targets[0].Lines[0] >= 1_000_000 {
		t.Fatalf("instant kill accepted for another skill: %+v", p.Targets[0])
	}
	if InstantKillLine(23110006, 0, mob, 999_999) {
		t.Fatal("a value other than the maximum HP is not an instant kill")
	}
}

// The Head Shots' only line against a non-boss is the per-line cap minus a
// random [0, 10000) (v148 0x71219A). Captured: 9,007,199,254,731,682 with the
// long-damage cap 2^53-1 (lane B long10, 5221016), which the world had
// clipped to its formula ceiling 32,507,996,750.
func TestHeadShotCapSentinelIsAnInstantKill(t *testing.T) {
	defer protocol.SetLongDamageCap(0)
	mob := content.Mob{HP: 100_000_000_000}
	protocol.SetLongDamageCap(protocol.LongDamageMaxCap)
	for _, id := range []int{3221007, 5221016, 5721006} {
		if !InstantKillLine(id, 0, mob, 9_007_199_254_731_682) {
			t.Fatalf("%d: captured long-mode sentinel refused", id)
		}
	}
	for _, claimed := range []int64{protocol.LongDamageMaxCap - 10_000, protocol.LongDamageMaxCap + 1, 32_507_996_750} {
		if InstantKillLine(5221016, 0, mob, claimed) {
			t.Fatalf("%d is not the sentinel", claimed)
		}
	}
	if InstantKillLine(5221016, 1, mob, 9_007_199_254_731_682) || InstantKillLine(5221016, 0, content.Mob{HP: 1, Boss: true}, 9_007_199_254_731_682) ||
		InstantKillLine(5221015, 0, mob, 9_007_199_254_731_682) {
		t.Fatal("sentinel accepted outside the first line, against a boss or for another skill")
	}
	protocol.SetLongDamageCap(0)
	if !InstantKillLine(5221016, 0, mob, 999_999-4_115) || InstantKillLine(5221016, 0, mob, 9_007_199_254_731_682) {
		t.Fatal("without long damage the sentinel is 999999 minus the roll")
	}
	protocol.SetLongDamageCap(50_000_000) // the earlier int-mode capture 49,995,885 had this cap armed
	if !InstantKillLine(5221016, 0, mob, 49_995_885) {
		t.Fatal("captured 50,000,000-cap sentinel refused")
	}

	c := warrior()
	s, _ := BaseStats(c, time.Now())
	protocol.SetLongDamageCap(protocol.LongDamageMaxCap)
	e := Effect{SkillID: 5221016, Level: 30, Damage: 1000, MobCount: 1, AttackCount: 4,
		Box: Box{LT: Point{-400, -60}, RB: Point{0, 10}, Set: true}}
	ctx := AttackContext{Kind: Shoot, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0),
		Attack:  attack(protocol.RecvShootAttack, 5221016, 0x0010, 0, map[int32][]uint32{5: {1}}),
		Targets: []TargetInfo{{ObjectID: 5, Mob: mob, X: -100, HP: mob.HP}}}
	ctx.Attack.Targets[0].Lines[0].Raw, ctx.Attack.Targets[0].Lines[0].Long = 0x7F000010, 9_007_199_254_731_682
	p, err := PlanAttack(nil, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Targets[0].Lines[0] != 9_007_199_254_731_682 || p.Targets[0].Clipped != 0 {
		t.Fatalf("sentinel line clipped: %+v", p.Targets[0])
	}
}
