package combat

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestResourcePlanPreparedOnceAndPaidSummon(t *testing.T) {
	now := time.Now()
	ctx := AttackContext{Kind: Melee, Character: domain.Character{HP: 10, MP: 7}, Stats: PlayerStats{ResourceCosts: content.ResourceCostModifiers{Prepared: true, MPConsumePercent: 150, MPReductionPercent: 33, CooldownReductionSeconds: 7}}, Effect: Effect{SkillID: 2121003, MPCon: 7, Cooltime: 10 * time.Second, MobCount: 1, AttackCount: 1, Damage: 100}, Now: now}
	called := 0
	spec := &AttackSpec{Kind: Melee, Prepare: func(c *AttackContext) error { called++; return nil }}
	p, err := PlanAttack(spec, ctx, nil)
	if err != nil || called != 1 || p.Cost.MP != 7 || p.Cooldown != 3*time.Second {
		t.Fatal(p, called, err)
	}
	spec.NoCooldown = true
	if p, err = PlanAttack(spec, ctx, nil); err != nil || p.Cooldown != 0 {
		t.Fatal(p, err)
	}
	ctx.Kind = Summoned
	ctx.Character.MP = 0
	ctx.Character.HP = 1
	ctx.Effect.HPCon = 100
	ctx.Effect.ItemCon = 2000000
	if _, err = PlanAttack(spec, ctx, nil); err == nil {
		t.Fatal("user attack claimed free summon")
	}
	spec = &AttackSpec{Kind: Summoned, Summon: &SummonSpec{}}
	if p, err = PlanAttack(spec, ctx, nil); err != nil || p.Cost != (Cost{}) || p.Cooldown != 0 {
		t.Fatal("cast paid summon blocked", p, err)
	}
	ctx.Kind = Melee
	spec = &AttackSpec{Kind: Melee}
	ctx.Effect.MPCon = 10
	ctx.Effect.HPCon = 0
	ctx.Effect.ItemCon = 0
	if _, err = PlanAttack(spec, ctx, nil); err == nil {
		t.Fatal("ordinary hit got paid-summon exemption")
	}
}

func TestResourceSnapshotMandatoryForActivePotentialAndForceKind(t *testing.T) {
	now := time.Now()
	ctx := AttackContext{Kind: Melee, Character: domain.Character{Job: 3112}, Effect: Effect{MPCon: 10, Cooltime: time.Minute}, Now: now}
	ctx.Stats.ResourceCosts = content.ResourceCostModifiers{Prepared: true, MPConsumePercent: 150, MPReductionPercent: 50, DemonForce: true, Infinity: true}
	if got, err := PreparedResourceCost(nil, ctx); err != nil || got.MP != 0 {
		t.Fatal("ordinary mpCon charged demon", got, err)
	}
	ctx.Effect.ForceCost = true
	if got, err := PreparedResourceCost(nil, ctx); err != nil || got.MP != 10 {
		t.Fatal("ordinary reducers affected DF", got, err)
	}
	ctx.Stats.ResourceCosts = content.ResourceCostModifiers{}
	ctx.Character.Equipment = []domain.Equipment{{Slot: -11, Potential: domain.EquipmentPotential{Present: true, RawState: 17}}}
	if _, err := PreparedResourceCost(nil, ctx); err == nil {
		t.Fatal("missing active potential snapshot silently fell back")
	}
	ctx.Attack = protocol.Attack{Opcode: protocol.RecvBodyAttack}
	ctx.Kind = Body
	ctx.Effect.SkillID = 1
	if !OngoingResourceCast(ctx) {
		t.Fatal("body cast not recognized")
	}
}
