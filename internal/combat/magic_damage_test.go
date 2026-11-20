package combat

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// A melee packet of a magicDamage skill is scored with the magic formula.
func TestMagicDamageSpecUsesMagicCeiling(t *testing.T) {
	c := warrior()
	c.Job, c.INT, c.Equipment = 3212, 900, []domain.Equipment{{Slot: -11, ItemID: 1382000, MATK: 300}}
	s, _ := BaseStats(c, time.Now())
	e := Effect{SkillID: 32001000, Level: 1, Damage: 130, MobCount: 8, AttackCount: 3,
		Box: Box{LT: Point{-150, -60}, RB: Point{0, 10}, Set: true}}
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0),
		Attack:  attack(protocol.RecvMeleeAttack, 32001000, 0x0020, 0, map[int32][]uint32{5: {999999}}),
		Targets: []TargetInfo{{ObjectID: 5, Mob: content.Mob{HP: 1_000_000}, X: -100, HP: 1_000_000}}}
	physical, _ := PlanAttack(&AttackSpec{Kind: Melee | Magic}, ctx, nil)
	magic, _ := PlanAttack(&AttackSpec{Kind: Melee | Magic, MagicDamage: true}, ctx, nil)
	want := CritCeiling(DamageContext{Stats: s, Effect: e, Mob: content.Mob{HP: 1_000_000}, Magic: true},
		LineCeiling(DamageContext{Stats: s, Effect: e, Mob: content.Mob{HP: 1_000_000}, Magic: true}))
	if magic.Targets[0].Lines[0] != want || physical.Targets[0].Lines[0] == want {
		t.Fatalf("magic %d physical %d want %d", magic.Targets[0].Lines[0], physical.Targets[0].Lines[0], want)
	}
}
