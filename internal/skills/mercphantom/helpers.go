package mercphantom

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"os"
	"time"
)

// Weapon families from the WZ weapon field (item ID / 10000).
const (
	dualBowgun = 152 // Mercedes (Skill.wz weapon 52)
	cane       = 136 // Phantom (Skill.wz weapon 36)
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

// learned returns the character's live level of a skill, 0 when not learned.
func learned(c domain.Character, now time.Time, id int) int {
	for _, s := range c.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

// values returns the WZ values of another learned skill, nil when not learned.
func values(c domain.Character, now time.Time, id int) map[string]int {
	level := learned(c, now, id)
	if level < 1 {
		return nil
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
	if err != nil {
		return nil
	}
	v, err := def.Values(min(level, def.MaxLevel))
	if err != nil {
		return nil
	}
	return v
}

// rule is the Java behaviour of one attack beyond the WZ values.
type rule struct {
	kind  combat.AttackKind
	onHit []combat.MobStatusApply
	// boost names the passives whose damR adds to this attack's damage
	// percent. Every learned one adds: the v148 client sums Aerial Barrage
	// and Rolling Moonsault on Rising Rush (Java replaced the first).
	boost []int
	// mobCount overrides the WZ target limit (Java final attacks use 6).
	mobCount  int
	maxDamage func(combat.DamageContext) int
	// levelFrom is the learned skill whose level authorizes a hidden linked
	// attack (Java GameConstants.getLinkedAttackSkill).
	levelFrom int
}

// attack builds the spec. Dual bowguns and canes fire no projectiles, so a
// Shoot attack never consumes ammo.
func attack(r rule) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: r.kind, OnHit: r.onHit, MaxDamage: r.maxDamage, Ammo: combat.AmmoNone, LevelFrom: r.levelFrom}
	if len(r.boost) > 0 || r.mobCount > 0 {
		s.Prepare = func(ctx *combat.AttackContext) error {
			if r.mobCount > 0 {
				ctx.Effect.MobCount = r.mobCount
			}
			for _, id := range r.boost {
				if v := values(ctx.Character, ctx.Now, id); v != nil {
					ctx.Effect.Damage += v["damR"]
				}
			}
			return nil
		}
	}
	return s
}

// register adds an attack; skills with monster statuses are also KindMobStatus.
func register(id int, r rule) {
	kind := skills.KindAttack
	if len(r.onHit) > 0 {
		kind |= skills.KindMobStatus
	}
	skills.Register(skills.Handler{ID: id, Kind: kind, Attack: attack(r), LevelFrom: r.levelFrom})
}

// stat is one buff stat and how its value comes from the WZ level values.
type stat struct {
	name  buffstat.Stat
	value func(map[string]int) int
}

func key(name buffstat.Stat, keys ...string) stat {
	return stat{name, func(v map[string]int) int {
		total := 0
		for _, k := range keys {
			total += v[k]
		}
		return total
	}}
}

func fixed(name buffstat.Stat, value int) stat {
	return stat{name, func(map[string]int) int { return value }}
}

// buff is the Cast of a self buff with the given stats for the WZ time;
// family, when non-zero, is the weapon the WZ weapon field requires.
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

func statBuff(c *skills.CastContext, stats []stat) (*skills.Buff, error) {
	if c.Value("time") < 1 {
		return nil, fmt.Errorf("skill%d: no duration", c.Skill.ID)
	}
	b := &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second}
	for _, s := range stats {
		if v := s.value(c.Values); v != 0 {
			b.Stats = append(b.Stats, skills.StatValue{Stat: s.name, Value: v})
		}
	}
	if len(b.Stats) == 0 {
		return nil, fmt.Errorf("skill%d: no buff stat", c.Skill.ID)
	}
	return b, nil
}

// passive registers an always-on bonus (nil for skills without a PvE effect).
func passive(id int, f func(*skills.PassiveContext) skills.Bonus) {
	skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
}

// weaponMastery is Java CalcPassive_Mastery: mastery and accX accuracy while
// family is equipped, replaced by expert once it is learned (Java picks
// 24120006 over 24100004 for canes).
func weaponMastery(family, expert int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		if weapon(c.Character) != family {
			return skills.Bonus{}
		}
		b := skills.Bonus{ACC: c.Values["accX"]}
		if learned(c.Character, c.Now, expert) < 1 {
			b.Mastery = c.Values["mastery"]
		}
		return b
	}
}

// expertMastery (23120009, 24120006) sets the weapon mastery and adds attack
// (x or padX) and criticaldamageMin.
func expertMastery(family int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		b := skills.Bonus{WATK: c.Values["x"] + c.Values["padX"], CriticalDamageMin: c.Values["criticaldamageMin"]}
		if weapon(c.Character) == family {
			b.Mastery = c.Values["mastery"]
		}
		return b
	}
}

// snipeCeiling is Java DamageParse for 23121003: a non-boss target may take
// its whole maximum HP in one line.
func snipeCeiling(c combat.DamageContext) int {
	if c.Mob.Boss || c.Mob.Invincible {
		return combat.LineCeiling(c)
	}
	return int(min(max(c.Mob.HP, int64(combat.LineCeiling(c))), int64(combat.MaxLineDamage())))
}
