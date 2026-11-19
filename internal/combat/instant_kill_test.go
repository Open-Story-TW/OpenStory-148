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
