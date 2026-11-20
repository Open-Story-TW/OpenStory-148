package combat

import (
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

const reviewedCrusherWire = "4b0001331901140000f3ee5a92004a8045095a94020676b8441b0000000060420f000781060186ffd70086ffd7005802aa010000140100005a010000eb01671405e3f28f5f420f000781060166ffd70066ffd7009e023b0100005801000027010000eb016714ac9cf4495e420f000781060146ffd70046ffd700e4023d010000f10100001a010000eb016714a642b6f7fdffd700"

func crusherContext(t *testing.T) AttackContext {
	t.Helper()
	raw, err := hex.DecodeString(reviewedCrusherWire)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:2:2], protocol.UpgradeV145AttackBody(protocol.RecvMeleeAttack, raw[2:])...) // archived v145 frame
	a, err := protocol.ReadAttack(protocol.RecvMeleeAttack, protocol.NewReader(raw[2:]))
	if err != nil {
		t.Fatal(err)
	}
	ctx := AttackContext{Kind: Melee, Character: domain.Character{Job: 132, Level: 150, HP: 3220, MP: 1454},
		Stats:  PlayerStats{Job: 132, Level: 150, STR: 57, DEX: 500, WATK: 32, WeaponID: 1432000, Mastery: 50, CritRate: 30, CritDamageMin: 15},
		Effect: Effect{SkillID: 1311001, Level: 20, Damage: 200, AttackCount: 3, MobCount: 3, MPCon: 24, Box: Box{LT: Point{-200, -80}, RB: Point{-16, 0}, Set: true}}, Attack: a, Now: time.Unix(100, 0)}
	for i, target := range a.Targets {
		ctx.Targets = append(ctx.Targets, TargetInfo{ObjectID: target.ObjectID, Mob: content.Mob{ID: 9300306, Level: 129, HP: 550000000, PDRate: 50, Boss: true}, X: []int{-122, -154, -186}[i], Y: 215, HP: 550000000, HalfWidth: 70, BodyHeight: 140})
	}
	return ctx
}

func TestReviewedCrusherRetainsAllNativeDamageWithoutInventingCriticalBits(t *testing.T) {
	ctx := crusherContext(t)
	dc := DamageContext{Stats: ctx.Stats, Effect: ctx.Effect, Mob: ctx.Targets[0].Mob}
	floor, ceiling := LineBounds(dc)
	if ceiling != 348 || CritCeiling(dc, ceiling) != 522 {
		t.Fatal("fixture no longer reproduces old bound", floor, ceiling)
	}
	before := ctx.Attack
	p, err := PlanAttack(&AttackSpec{Kind: Melee}, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int{{426, 276, 346}, {315, 344, 295}, {317, 497, 282}}
	var total int64
	for i, target := range p.Targets {
		if !reflect.DeepEqual(target.Lines, want[i]) || target.Clipped != 0 {
			t.Fatal("legitimate native line clipped", target)
		}
		for j, state := range target.Criticality {
			if state != domain.CombatCriticalUnknown || ctx.Attack.Targets[i].Lines[j].Critical() {
				t.Fatal("unknown line falsely classified")
			}
		}
		total += target.Total()
	}
	if total != 3098 || p.Cost.MP != 24 || !reflect.DeepEqual(ctx.Attack, before) {
		t.Fatal("damage/cost/raw changed", total, p.Cost)
	}
}

func TestNativeBossCriticalAccumulatesByPhysicalLineAndResetsPerTarget(t *testing.T) {
	ctx := crusherContext(t)
	ctx.Stats.CritRate, ctx.Stats.BossCritRate = 15, 20
	ctx.Targets[1].Mob.Boss = false
	plan, err := PlanAttack(&AttackSpec{Kind: Melee}, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, target := range plan.Targets {
		firstRate := ctx.Stats.CriticalRateAgainst(ctx.Effect.CriticalRate, ctx.Targets[i].Mob.Boss)
		want := []int{35, 55, 75, 95, 95, 95, 95, 95}
		if i == 1 {
			want = []int{15, 15, 15, 15, 15, 15, 15, 15}
		}
		for index, rate := range want {
			if got := target.LineCriticalRate(firstRate, index); got != rate {
				t.Fatalf("target %d connected hit %d rate = %d, want %d", i, index, got, rate)
			}
		}
	}
	next, err := PlanAttack(&AttackSpec{Kind: Melee}, ctx, nil)
	if err != nil || next.Targets[0].LineCriticalRate(35, 0) != 35 {
		t.Fatalf("the first hit of a new cast did not reset: %+v, %v", next.Targets, err)
	}
	ctx.Kind = Magic
	magic, err := PlanAttack(&AttackSpec{Kind: Magic}, ctx, nil)
	if err != nil || magic.Targets[0].LineCriticalRate(35, 3) != 35 {
		t.Fatalf("magic boss critical rate changed: %+v, %v", magic.Targets, err)
	}
}

func TestCriticalAdmissionUsesEachTargetsBossAndActiveStunState(t *testing.T) {
	ctx := crusherContext(t)
	ctx.Stats.CritRate, ctx.Stats.BossCritRate, ctx.Stats.StunnedCritRate = 15, 20, 60
	ctx.Effect.CriticalRate = 7
	ctx.Targets[1].Mob.Boss, ctx.Targets[2].Mob.Boss = false, false
	ctx.Targets[1].Statuses = MobStatuses{MobStun: &ActiveStatus{Stat: MobStun, ExpiresAt: ctx.Now.Add(time.Second)}}
	ctx.Targets[2].Statuses = MobStatuses{MobStun: &ActiveStatus{Stat: MobStun, ExpiresAt: ctx.Now}}
	p, err := PlanAttack(&AttackSpec{Kind: Melee}, ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{42, 82, 22} {
		if got := p.Targets[i].CriticalRate; got != want {
			t.Errorf("target %d critical rate: got %d want %d", i, got, want)
		}
		for _, state := range p.Targets[i].Criticality {
			if state != domain.CombatCriticalUnknown {
				t.Fatal("target-specific rate inferred a native critical flag")
			}
		}
	}
}

func TestReviewedCriticalPolicyKeepsFixedAndOtherBranchesAndFiniteCap(t *testing.T) {
	ctx := crusherContext(t)
	dc := DamageContext{Stats: ctx.Stats, Effect: ctx.Effect, Mob: ctx.Targets[0].Mob}
	for _, tc := range []struct {
		name   string
		kind   AttackKind
		change func(*DamageContext)
		want   int
	}{
		{"crusher", Melee, func(*DamageContext) {}, 522},
		{"otherSkill", Melee, func(c *DamageContext) { c.Effect.SkillID = 1311003 }, 522},
		{"otherFamily", Magic, func(*DamageContext) {}, 522},
		{"summoned", Summoned, func(*DamageContext) {}, 348},
		{"fixedSkill", Melee, func(c *DamageContext) { c.Effect.FixDamage = 10 }, 10},
		{"fixedMob", Melee, func(c *DamageContext) { c.Mob.HasFixedDamage = true; c.Mob.FixedDamage = 7 }, 7},
		{"invincible", Melee, func(c *DamageContext) { c.Mob.Invincible = true }, 0},
		{"normalOnly", Melee, func(c *DamageContext) { c.Mob.OnlyNormalAttack = true }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dc
			tc.change(&c)
			f, max := LineBounds(c)
			_, max = WireLineBounds(c, tc.kind, 999999, f, max)
			if max != tc.want {
				t.Fatal(max, tc.want)
			}
		})
	}
	ctx.Attack.Targets[0].Lines[0].Raw = 999999
	p, err := PlanAttack(&AttackSpec{Kind: Melee}, ctx, nil)
	if err != nil || p.Targets[0].Lines[0] != 522 || p.Targets[0].Clipped != 1 {
		t.Fatal("unknown did not retain finite bound", p, err)
	}
	if ReviewedWireCriticality(Melee, 1311001, 0x800001f1) != domain.CombatCriticalExplicit || ReviewedWireCriticality(Melee, 1311003, 497) != "" {
		t.Fatal("classification scope widened")
	}
}
