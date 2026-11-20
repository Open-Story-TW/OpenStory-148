package combat

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

// A spec's LineDamage bounds one line with its own skill percent (the v148
// physical calc swaps the percent of Exploding Arrows' last line to x).
func TestLineDamageBoundsTheSwappedLine(t *testing.T) {
	c := warrior()
	s, _ := BaseStats(c, time.Now())
	e := Effect{SkillID: 1001005, Level: 1, Damage: 130, X: 500, MobCount: 8, AttackCount: 3,
		Box: Box{LT: Point{-150, -60}, RB: Point{0, 10}, Set: true}}
	mob := content.Mob{HP: 1_000_000}
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0),
		Attack:  attack(protocol.RecvMeleeAttack, 1001005, 0x0020, 0, map[int32][]uint32{5: {999999, 999999, 999999}}),
		Targets: []TargetInfo{{ObjectID: 5, Mob: mob, X: -100, HP: 1_000_000}}}
	bound := func(pct int) int {
		dc := DamageContext{Stats: s, Effect: e, Mob: mob}
		dc.Effect.Damage = pct
		return CritCeiling(dc, LineCeiling(dc))
	}
	spec := &AttackSpec{Kind: Melee, LineDamage: func(e Effect, line, lines int) int {
		if line == lines-1 {
			return e.X
		}
		return 0
	}}
	p, err := PlanAttack(spec, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := p.Targets[0].Lines
	if got[0] != bound(130) || got[1] != bound(130) || got[2] != bound(500) || got[2] <= got[0] {
		t.Fatalf("lines %v want %d %d %d", got, bound(130), bound(130), bound(500))
	}
	if p, _ := PlanAttack(&AttackSpec{Kind: Melee}, ctx, nil); p.Targets[0].Lines[2] != bound(130) {
		t.Fatalf("without LineDamage the last line %d want %d", p.Targets[0].Lines[2], bound(130))
	}
}
