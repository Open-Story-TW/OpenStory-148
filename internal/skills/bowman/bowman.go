package bowman

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"time"
)

// Weapon families from the WZ weapon field (item ID / 10000).
const (
	bow      = 145
	crossbow = 146
)

// weapon returns the family of the equipped weapon (slot -11), 0 when none.
func weapon(c domain.Character) int {
	for _, e := range c.Equipment {
		if e.Slot == -11 {
			return e.ItemID / 10000
		}
	}
	return 0
}

// learned returns the character's level of a skill, 0 when not learned.
func learned(c domain.Character, id int) int {
	for _, s := range c.Skills {
		if s.ID == id {
			return s.Level
		}
	}
	return 0
}

// buffed reports an unexpired buff stat.
func buffed(c domain.Character, name buffstat.Stat, now time.Time) bool {
	for _, b := range c.SkillBuffs {
		if b.Stat == string(name) && b.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// rule is the Java behaviour of one bowman attack beyond the WZ values.
type rule struct {
	noAmmo    bool // Java AOE: Arrow Rain / Arrow Eruption shoot no arrows.
	onHit     []combat.MobStatusApply
	prepare   func(*combat.AttackContext) error
	maxDamage func(combat.DamageContext) int
	drain     func(*combat.AttackContext, int64) (int, int)
}

// shoot is the RecvShootAttack spec of a bowman attack. Soul Arrow (an active
// SOULARROW buff) shoots without arrows, like Java rangedAttack.
func shoot(r rule) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: combat.Shoot, OnHit: r.onHit, MaxDamage: r.maxDamage, Drain: r.drain}
	if r.noAmmo {
		s.Ammo = combat.AmmoNone
	}
	s.Prepare = func(ctx *combat.AttackContext) error {
		if buffed(ctx.Character, "SOULARROW", ctx.Now) {
			ctx.NoAmmo = true
		}
		if r.prepare != nil {
			return r.prepare(ctx)
		}
		return nil
	}
	return s
}

// stun is Java's STUN 1 for the WZ time at the WZ prop.
var stun = []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1}}

// element sets the attack element (Skill.wz elemAttr, not yet in the seed).
func element(e combat.Element) func(*combat.AttackContext) error {
	return func(ctx *combat.AttackContext) error {
		ctx.Effect.Element = e
		return nil
	}
}

// buff is the Cast of a self buff with the given stats for time seconds;
// each stat takes the named WZ value. family, when non-zero, is the weapon the
// WZ weapon field requires.
func buff(family int, stats ...stat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		if family != 0 && weapon(c.Caster) != family {
			return p, fmt.Errorf("skill%d: needs weapon %d", c.Skill.ID, family)
		}
		b, e := statBuff(c, stats)
		p.Self = b
		return p, e
	}
}

// partyBuff is buff applied to every party member inside the WZ lt/rb.
func partyBuff(stats ...stat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		b, e := statBuff(c, stats)
		if e != nil {
			return skills.StandardCost(c), e
		}
		return skills.PartyPlan(c, b, skills.StandardCost(c))
	}
}

// stat is one buff stat and how its value comes from the WZ level values.
type stat struct {
	name  buffstat.Stat
	value func(map[string]int) int
}

func key(name buffstat.Stat, k string) stat {
	return stat{name, func(v map[string]int) int { return v[k] }}
}

func statBuff(c *skills.CastContext, stats []stat) (*skills.Buff, error) {
	if c.Value("time") < 1 {
		return nil, fmt.Errorf("skill%d: no duration", c.Skill.ID)
	}
	b := &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second}
	for _, s := range stats {
		v := s.value(c.Values)
		if v == 0 {
			return nil, fmt.Errorf("skill%d: %s is zero", c.Skill.ID, s.name)
		}
		b.Stats = append(b.Stats, skills.StatValue{Stat: s.name, Value: v})
	}
	return b, nil
}
