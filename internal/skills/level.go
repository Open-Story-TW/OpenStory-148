package skills

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"os"
	"time"
)

// EffectiveLevel adds active COMBAT_ORDERS to learned levels within the WZ
// limits. Native86CF1C excludes beginner skills and Combat Orders1211011 itself.
// Missing/expired skills return zero. Potential increments and the remaining
// native special getter branches require their own complete source policy.
func EffectiveLevel(c domain.Character, id int, now time.Time) int {
	return levelFor(c, id, id, now)
}

// CastLevel validates a client-sent level for skill id, whose level is
// learned through learnedID (Handler.LevelFrom, else id). The client sends
// the effective level; the plain learned level is also accepted while a
// COMBAT_ORDERS change is in flight. It returns the level to use.
func CastLevel(c domain.Character, learnedID, id, client int, now time.Time) (int, bool) {
	base := learnedLevel(c, learnedID, now)
	if base < 1 || client < 1 {
		return 0, false
	}
	if client == levelFor(c, learnedID, id, now) {
		return client, true
	}
	if client != base {
		return 0, false
	}
	if def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id); err == nil {
		return min(base, def.MaxLevel), true // A linked skill may cap below its parent.
	}
	return base, true
}

// CombatOrders returns the active COMBAT_ORDERS buff value of c.
func CombatOrders(c domain.Character, now time.Time) int {
	for _, b := range c.SkillBuffs {
		if b.Stat == "COMBAT_ORDERS" && b.ExpiresAt.After(now) {
			return max(0, b.Value)
		}
	}
	return 0
}

func learnedLevel(c domain.Character, id int, now time.Time) int {
	for _, s := range c.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

func levelFor(c domain.Character, learnedID, id int, now time.Time) int {
	base := learnedLevel(c, learnedID, now)
	if base < 1 {
		return 0
	}
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), id)
	if err != nil {
		return base
	}
	bonus := 0
	if id != 1211011 && !domain.IsBeginnerJob(id/10000) {
		bonus = CombatOrders(c, now)
	}
	if bonus == 0 {
		return min(base, def.MaxLevel)
	}
	return min(base+bonus, def.TrueMax())
}
