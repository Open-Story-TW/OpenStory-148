package bowman

import (
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func TestMortalBlow(t *testing.T) {
	c := domain.Character{Job: 312, MaxHP: 10000, Skills: []domain.SkillState{{ID: 3110001, Level: 15}}} // x 30, z 5
	dc := combat.DamageContext{Attacker: c, Mob: content.Mob{HP: 100000}, HP: 50000}
	if got := mortalCeiling(dc, 1000); got != 1000 {
		t.Errorf("above x%%: %d", got)
	}
	dc.HP = 29000
	if got := mortalCeiling(dc, 1000); got != 29000 {
		t.Errorf("below x%%: %d", got)
	}
	dc.Attacker.Job = 322 // the crossbow line needs 3210001
	if got := mortalCeiling(dc, 1000); got != 1000 {
		t.Errorf("marksman without 3210001: %d", got)
	}
	ctx := &combat.AttackContext{Character: c, Targets: []combat.TargetInfo{
		{Mob: content.Mob{HP: 100000}, HP: 20000}, {Mob: content.Mob{HP: 100000}, HP: 90000}, {Mob: content.Mob{HP: 100000}, HP: 1000},
	}}
	if hp, mp := mortalHeal(ctx, 0); hp != 1000 || mp != 0 {
		t.Errorf("heal %d/%d, want 2 targets × 5%% of 10000", hp, mp)
	}
	// Wind Archer 13110009 Mortal Blow (v148 0x60b6cd alongside 3110001/3210001).
	wa := domain.Character{Job: 1311, MaxHP: 10000, Skills: []domain.SkillState{{ID: 13110009, Level: 15}}}
	dcWA := combat.DamageContext{Attacker: wa, Mob: content.Mob{HP: 100000}, HP: 20000}
	if got := mortalCeiling(dcWA, 1000); got != 20000 {
		t.Errorf("wind archer 13110009 below x%%: %d, want 20000", got)
	}
	if hp, mp := mortalHeal(&combat.AttackContext{Character: wa, Targets: ctx.Targets}, 0); hp < 1 || mp != 0 {
		t.Errorf("wind archer 13110009 heal %d/%d, want >0 HP", hp, mp)
	}
}

func TestSpiritLinkPassiveFollowsTheSummonBuff(t *testing.T) {
	def, _ := content.SkillDefinition("", 3120006)
	v, _ := def.Values(30)
	now := time.Unix(1000, 0)
	ctx := &skills.PassiveContext{Skill: def, Level: 30, Values: v, Character: domain.Character{Job: 312}, Now: now}
	if b := spiritLink(3111005)(ctx); b != (skills.Bonus{}) {
		t.Fatalf("no phoenix %+v", b)
	}
	ctx.Character.SkillBuffs = []domain.SkillBuff{{ID: 3111005, Stat: "SUMMON", Value: 1, ExpiresAt: now.Add(time.Minute)}}
	if b := spiritLink(3111005)(ctx); b != (skills.Bonus{}) {
		t.Fatalf("summon with no active Spirit Link slot: %+v", b)
	}
	for _, name := range []string{"SPIRIT_SURGE", "SPIRIT_LINK"} {
		ctx.Character.SkillBuffs = append(ctx.Character.SkillBuffs[:1], domain.SkillBuff{ID: 3111005, Stat: name, Value: 1, ExpiresAt: now.Add(time.Minute)})
		if b := spiritLink(3111005)(ctx); b != (skills.Bonus{MaxHPPercent: 40, WATKPercent: 30, MATKPercent: 30, DamagePercent: 15}) {
			t.Fatalf("phoenix up using %s: %+v", name, b)
		}
	}
}

func TestUltimateInfernoIsAnAttack(t *testing.T) {
	h, ok := skills.Lookup(3120010)
	if !ok || h.Attack == nil || !h.Attack.Allows(combat.Shoot) || h.OnDamaged != nil {
		t.Fatalf("%+v", h)
	}
}
