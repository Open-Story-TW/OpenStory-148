package content

import (
	"maple-go-server/internal/domain"
	"testing"
	"time"
)

func TestFirstWarriorPhysicalMetadataAndDamageAuthority(t *testing.T) {
	c := domain.Character{Job: 100, Level: 10, HP: 50, STR: 50, DEX: 4, Equipment: []domain.Equipment{{Slot: -11, ItemID: 1302000, WATK: 20}, {Slot: -111, ItemID: 1702000, WATK: 9999}}, Skills: []domain.SkillState{{ID: 1001004, Level: 1}, {ID: 1001005, Level: 1}}}
	p, err := PhysicalAttackProfileFor("", c, 1001004, time.Now())
	if err != nil || p.MP != 5 || p.DamagePercent != 185 || p.MaxTargets != 1 {
		t.Fatalf("power strike %+v %v", p, err)
	}
	maximum, err := p.MaximumDamage(Mob{PDRate: 10})
	if err != nil || maximum != 82 {
		t.Fatalf("source physical ceiling=%d %v", maximum, err)
	}
	p, err = PhysicalAttackProfileFor("", c, 1001005, time.Now())
	if err != nil || p.MP != 6 || p.DamagePercent != 125 || p.MaxTargets != 6 || p.Range != 180 {
		t.Fatalf("slash blast %+v %v", p, err)
	}
	if _, err = p.MaximumDamage(Mob{OnlyNormalAttack: true}); err == nil {
		t.Fatal("skill accepted on normal-only monster")
	}
	if _, err = p.MaximumDamage(Mob{Invincible: true}); err == nil {
		t.Fatal("invincible monster accepted")
	}
	if _, err = p.MaximumDamage(Mob{HasFixedDamage: true, FixedDamage: 0}); err == nil {
		t.Fatal("explicit zero fixed-damage policy was treated as absent")
	}
	if maximum, err = p.MaximumDamage(Mob{PDRate: 100}); err != nil || maximum != 0 {
		t.Fatal("100-percent physical resistance was forced to take one damage")
	}
	zero := &wzNode{Children: []wzNode{{Name: "fixedDamage", Value: "0"}}}
	if !parseMob(123, zero).HasFixedDamage || parseMob(123, &wzNode{}).HasFixedDamage {
		t.Fatal("WZ fixedDamage presence was not retained")
	}
	c.Skills[1].ExpiresAt = time.Now().Add(-time.Second)
	if _, err = PhysicalAttackProfileFor("", c, 1001005, time.Now()); err == nil {
		t.Fatal("expired physical skill accepted")
	}
	c.Job = 0
	if _, err = PhysicalAttackProfileFor("", c, 1001004, time.Now()); err == nil {
		t.Fatal("beginner used warrior skill")
	}
}
