package pirate

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestV148RegionalBookAttacksFollowTheNativeSender(t *testing.T) {
	for _, row := range []struct {
		id     int
		kind   combat.AttackKind
		noAmmo bool
		stun   bool
	}{
		{5091001, combat.Melee, false, false},
		{5091002, combat.Melee, false, false},
		{5811006, combat.Melee, false, false},
		{5821001, combat.Melee, false, false},
		{5821002, combat.Shoot, true, false},
		{5821005, combat.Melee, false, true},
		{5821007, combat.Melee, false, false},
		{5901001, combat.Shoot, true, false},
		{5911004, combat.Shoot, false, false},
		{5920011, combat.Shoot, false, false},
		{5921003, combat.Melee, false, false},
		{5921004, combat.Shoot, true, false},
	} {
		h, ok := skills.Lookup(row.id)
		if !ok || h.Attack == nil || h.Attack.Kind != row.kind || (h.Attack.Ammo == combat.AmmoNone) != row.noAmmo {
			t.Fatalf("%d: wrong sender or ammo", row.id)
		}
		if stun := len(h.Attack.OnHit) == 1 && h.Attack.OnHit[0].Stat == combat.MobStun; stun != row.stun {
			t.Fatalf("%d: wrong stun", row.id)
		}
	}
	if h, _ := skills.Lookup(5920011); h.OnAttack == nil {
		t.Fatal("5920011 does not mark its target")
	}
}

func TestV148RegionalBookAttackRequirements(t *testing.T) {
	now := time.Unix(100, 0)
	buff := func(id int, stat string, value int) *combat.AttackContext {
		return &combat.AttackContext{Now: now, Character: domain.Character{SkillBuffs: []domain.SkillBuff{
			{ID: id, Stat: stat, Value: value, ExpiresAt: now.Add(time.Minute)}}}}
	}
	shockwave, _ := skills.Lookup(5811006)
	for _, ok := range []*combat.AttackContext{buff(5811005, "MORPH", 1000), buff(5821003, "MORPH", 1001)} {
		if err := shockwave.Attack.Prepare(ok); err != nil {
			t.Fatalf("shockwave in a transformation: %v", err)
		}
	}
	for _, bad := range []*combat.AttackContext{{Now: now}, buff(5111005, "MORPH", 1000), buff(5811005, "MORPH", 1001)} {
		if shockwave.Attack.Prepare(bad) == nil {
			t.Fatal("shockwave without the regional transformation")
		}
	}
	snatch, _ := skills.Lookup(5821005)
	if snatch.Attack.Prepare(buff(5811005, "MORPH", 1000)) == nil || snatch.Attack.Prepare(buff(5821003, "MORPH", 1001)) != nil {
		t.Fatal("snatch needs the super transformation")
	}
	orb, _ := skills.Lookup(5821002)
	if orb.Attack.Prepare(buff(5810001, "ENERGY_CHARGE", energyFull-1)) == nil || orb.Attack.Prepare(buff(5810001, "ENERGY_CHARGE", energyFull)) != nil {
		t.Fatal("energy orb needs a full regional gauge")
	}
	flame, _ := skills.Lookup(5911004)
	def, _ := values(t, 5911004, 20)
	e, err := combat.FromSkill(def, 20)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &combat.AttackContext{Effect: e}
	if err = flame.Attack.Prepare(ctx); err != nil || ctx.Effect.Element != combat.Fire ||
		len(ctx.Effect.MonsterStatus) != 1 || ctx.Effect.MonsterStatus[0].Stat != combat.MobPoison {
		t.Fatalf("flamethrower %+v (%v)", ctx.Effect, err)
	}
}

func TestV148RegionalBookCasts(t *testing.T) {
	if p := cast(t, 5801005, 10); p.HPCost != 500 || p.Heal == nil || p.Heal.MP != 300 || p.Cooldown != 10*time.Second {
		t.Fatalf("mp recovery %+v", p)
	}
	if p := cast(t, 5801007, 10); stats(p.Self)["MORPH"] != 1002 || p.Self.Duration != 30*time.Second {
		t.Fatalf("oak barrel %+v", p.Self)
	}
	defer func(orig func(int) int) { rollDie = orig }(rollDie)
	rollDie = func(int) int { return 3 }
	if p := cast(t, 5811007, 20); stats(p.Self)["DICE_ROLL"] != 3 {
		t.Fatalf("lucky dice %+v", p.Self)
	}
	if p := cast(t, 5911002, 30); stats(p.Self)["SUMMON"] != 1 {
		t.Fatalf("gaviota %+v", p.Self)
	}
	if h, _ := skills.Lookup(5911002); h.Attack == nil || h.Attack.Summon == nil || !h.Attack.Summon.SingleAttack {
		t.Fatal("gaviota is not a single-attack summon")
	}
	if b := passive(t, 5920001, 30, domain.Character{Job: 592}); b.SkillDamage[0] != (skills.SkillDamage{Skill: 5911004, Percent: 80}) ||
		b.SkillDamage[1] != (skills.SkillDamage{Skill: 5911005, Percent: 80}) {
		t.Fatalf("regional elemental boost %+v", b.SkillDamage)
	}
}
