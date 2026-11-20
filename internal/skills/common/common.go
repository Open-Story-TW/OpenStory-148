package common

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"time"
)

// families are the beginner jobs that share one skill list by suffix (Java
// GameConstants.isBeginnerJob, sourceid % 10000): Explorer, Cygnus, Aran,
// Evan, Mercedes, Phantom, Resistance, Demon and Mihile.
var families = []int{0, 1000, 2000, 2001, 2002, 2003, 3000, 3001, 5000}

// each registers h for every beginner family whose Skill.wz has the suffix
// enabled. h.ID is overwritten.
func each(suffix int, h skills.Handler) {
	for _, f := range families {
		if id := f*10000 + suffix; enabled(id) {
			h.ID = id
			skills.Register(h)
		}
	}
}

// enabled checks the embedded seed and the Mihile entries whose original v148
// WZ level and behavior definitions match these shared beginner handlers.
func enabled(id int) bool {
	if id/10000 == 5000 {
		switch id % 10000 {
		case 86, 88, 91, 180, 190, 1000, 1001, 1002, 1003, 1005, 1006, 1010, 1011, 1026, 1085, 1087, 1090, 1098, 1179:
			return true
		default:
			return false
		}
	}
	def, err := content.SkillDefinition("", id)
	return err == nil && !def.Disabled
}

// stat is one buff stat and how its value comes from the WZ level values.
type stat struct {
	name  buffstat.Stat
	value func(map[string]int) int
}

func key(name buffstat.Stat, k string) stat {
	return stat{name, func(v map[string]int) int { return v[k] }}
}

func fixed(name buffstat.Stat, n int) stat {
	return stat{name, func(map[string]int) int { return n }}
}

// buff is the Cast of a self buff with the given stats for the WZ time (or
// duration when non-zero, Java's 2100000000 overrides); each stat must be
// non-zero.
func buff(duration time.Duration, stats ...stat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d := duration
		if d == 0 {
			d = time.Duration(c.Value("time")) * time.Second
		}
		if d <= 0 {
			return p, fmt.Errorf("skill%d: no duration", c.Skill.ID)
		}
		b := &skills.Buff{Duration: d}
		for _, s := range stats {
			v := s.value(c.Values)
			if v == 0 {
				return p, fmt.Errorf("skill%d: %s is zero", c.Skill.ID, s.name)
			}
			b.Stats = append(b.Stats, skills.StatValue{Stat: s.name, Value: v})
		}
		p.Self = b
		return p, nil
	}
}

// gmOnly wraps a Cast so that only GM characters may use it: Legacy bans a
// non-GM who sends a GM skill (MapleStatEffect.applyTo, also 1010).
func gmOnly(cast func(*skills.CastContext) (skills.CastPlan, error)) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		if c.Caster.GM < 1 {
			return skills.CastPlan{}, fmt.Errorf("skill%d: GM only", c.Skill.ID)
		}
		return cast(c)
	}
}
