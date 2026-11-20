package combat

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// A melee packet is scored with the magic formula for a magicDamage spec or
// a magician-branch job (the v148 melee sender's own choice).
func TestMagicDamageSpecUsesMagicCeiling(t *testing.T) {
	c := warrior()
	c.Job, c.INT, c.Equipment = 3212, 900, []domain.Equipment{{Slot: -11, ItemID: 1382000, MATK: 300}}
	s, _ := BaseStats(c, time.Now())
	e := Effect{SkillID: 32001000, Level: 1, Damage: 130, MobCount: 8, AttackCount: 3,
		Box: Box{LT: Point{-150, -60}, RB: Point{0, 10}, Set: true}}
	mob := content.Mob{HP: 1_000_000}
	ctx := AttackContext{Kind: Melee, Character: c, Stats: s, Effect: e, Now: time.Unix(100, 0),
		Attack:  attack(protocol.RecvMeleeAttack, 32001000, 0x0020, 0, map[int32][]uint32{5: {999999}}),
		Targets: []TargetInfo{{ObjectID: 5, Mob: mob, X: -100, HP: 1_000_000}}}
	want := CritCeiling(DamageContext{Stats: s, Effect: e, Mob: mob, Magic: true},
		LineCeiling(DamageContext{Stats: s, Effect: e, Mob: mob, Magic: true}))
	line := func(job int, spec *AttackSpec) int {
		x := ctx
		x.Character.Job = job
		p, err := PlanAttack(spec, x, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p.Targets[0].Lines[0]
	}
	if got := line(3212, &AttackSpec{Kind: Melee | Magic}); got != want {
		t.Fatalf("battle mage melee packet %d want magic %d", got, want)
	}
	if got := line(3112, &AttackSpec{Kind: Melee | Magic, MagicDamage: true}); got != want {
		t.Fatalf("magicDamage spec %d want %d", got, want)
	}
	if got := line(3112, &AttackSpec{Kind: Melee | Magic}); got == want {
		t.Fatalf("a non-magician melee packet used the magic formula: %d", got)
	}
}

func TestNativeMagicMeleeFollowsTheClientPredicate(t *testing.T) {
	for _, tc := range []struct {
		job, skill int
		want       bool
	}{
		{3212, 32101001, true}, {3200, 32001000, true}, {212, 2121006, true},
		{3212, 0, false}, {112, 1121008, false}, {2412, 2121006, true}, {2412, 24121000, false},
	} {
		if got := NativeMagicMelee(tc.job, tc.skill); got != tc.want {
			t.Errorf("job %d skill %d: %v", tc.job, tc.skill, got)
		}
	}
}
