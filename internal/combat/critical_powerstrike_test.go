package combat

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Original R4 native frame: local-hit critical=true, raw bit31 clear.
// Keep the frame intact; the local critical observation is not an input to PlanAttack.
const reviewedPowerStrikeR4Wire = "550001112c460f0000573f0488001480486874860206b606d41c0000000041420f0007810301afffd700afffd70090018d020000eb0167146cc0ce45ffffd700"

func reviewedPowerStrikeContext(t *testing.T) AttackContext {
	t.Helper()
	c := crusherContext(t)
	wire, err := hex.DecodeString(reviewedPowerStrikeR4Wire)
	if err != nil {
		t.Fatal(err)
	}
	if h := sha256.Sum256(wire[2:]); hex.EncodeToString(h[:]) != "b28dc281499ec686a24bd52c4cd2b8a1e19f70393a5715cb97ef60e1e2759436" {
		t.Fatal("original R4 body changed")
	}
	wire = append(wire[:2:2], protocol.UpgradeV145AttackBody(protocol.RecvMeleeAttack, wire[2:])...) // archived v145 frame
	c.Attack, err = protocol.ReadAttack(protocol.RecvMeleeAttack, protocol.NewReader(wire[2:]))
	if err != nil {
		t.Fatal(err)
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), 1001004)
	if err != nil {
		t.Fatal(err)
	}
	c.Effect, err = FromSkill(def, 20)
	if err != nil || c.Effect.Damage != 280 || c.Effect.MPCon != 11 {
		t.Fatal("actual Power Strike WZ changed", c.Effect, err)
	}
	c.Targets = c.Targets[:1]
	c.Targets[0].ObjectID = c.Attack.Targets[0].ObjectID
	c.Targets[0].X = -81
	return c
}

func TestReviewedPowerStrikeR4UnmarkedCriticalPreserves653AndFiniteCap(t *testing.T) {
	c := reviewedPowerStrikeContext(t)
	dc := DamageContext{Stats: c.Stats, Effect: c.Effect, Mob: c.Targets[0].Mob}
	_, normal := LineBounds(dc)
	if normal != 486 || CritCeiling(dc, normal) != 729 {
		t.Fatal("R4 normal/critical bounds changed", normal, CritCeiling(dc, normal))
	}
	before := c.Attack
	p, err := PlanAttack(&AttackSpec{Kind: Melee}, c, nil)
	if err != nil || len(p.Targets) != 1 || !reflect.DeepEqual(p.Targets[0].Lines, []int{653}) || p.Targets[0].Clipped != 0 || p.Cost.MP != 11 {
		t.Fatal("legitimate R4 line or cost changed", p, err)
	}
	if p.Targets[0].Criticality[0] != domain.CombatCriticalUnknown || c.Attack.Targets[0].Lines[0].Raw != 653 || c.Attack.Targets[0].Lines[0].Critical() || !reflect.DeepEqual(c.Attack, before) {
		t.Fatal("native raw/classification changed")
	}
	c.Attack.Targets[0].Lines[0].Raw = 0x8000028d
	p, err = PlanAttack(&AttackSpec{Kind: Melee}, c, nil)
	if err != nil || p.Targets[0].Lines[0] != 653 || p.Targets[0].Criticality[0] != domain.CombatCriticalExplicit {
		t.Fatal("genuine explicit bit lost or rewritten as unknown", p, err)
	}
	c.Attack.Targets[0].Lines[0].Raw = 730
	p, err = PlanAttack(&AttackSpec{Kind: Melee}, c, nil)
	if err != nil || p.Targets[0].Lines[0] != 729 || p.Targets[0].Clipped != 1 {
		t.Fatal("above legal critical ceiling not clipped", p, err)
	}
}

func TestReviewedPowerStrikeRetainsOtherFamiliesFixedImmuneAndExplicitPolicy(t *testing.T) {
	c := reviewedPowerStrikeContext(t)
	base := DamageContext{Stats: c.Stats, Effect: c.Effect, Mob: c.Targets[0].Mob}
	for _, tc := range []struct {
		name   string
		kind   AttackKind
		change func(*DamageContext)
		want   int
	}{
		{"powerstrike", Melee, func(*DamageContext) {}, 729},
		{"slashblast", Melee, func(c *DamageContext) { c.Effect.SkillID = 1001005 }, 729},
		{"otherSkill", Melee, func(c *DamageContext) { c.Effect.SkillID = 1311003 }, 729},
		{"shoot", Shoot, func(*DamageContext) {}, 729},
		{"magic", Magic, func(*DamageContext) {}, 729},
		{"summoned", Summoned, func(*DamageContext) {}, 486},
		{"fixedSkill", Melee, func(c *DamageContext) { c.Effect.FixDamage = 10 }, 10},
		{"fixedMob", Melee, func(c *DamageContext) { c.Mob.HasFixedDamage = true; c.Mob.FixedDamage = 7 }, 7},
		{"immune", Melee, func(c *DamageContext) { c.Mob.Invincible = true }, 0},
		{"normalOnly", Melee, func(c *DamageContext) { c.Mob.OnlyNormalAttack = true }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			tc.change(&d)
			f, max := LineBounds(d)
			_, max = WireLineBounds(d, tc.kind, 653, f, max)
			if max != tc.want {
				t.Fatal(max, tc.want)
			}
		})
	}
	if ReviewedWireCriticality(Melee, 1001004, 0x8000028d) != domain.CombatCriticalExplicit {
		t.Fatal("explicit wire bit lost")
	}
	if ReviewedWireCriticality(Melee, 1001005, 653) != "" || ReviewedWireCriticality(Shoot, 1001004, 653) != "" || ReviewedWireCriticality(Magic, 1001004, 653) != "" {
		t.Fatal("reviewed scope widened")
	}
}
