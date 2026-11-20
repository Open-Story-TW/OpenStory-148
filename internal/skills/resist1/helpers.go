package resist1

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"time"
)

// Weapon categories (item id / 10000).
const (
	oneHandedAxe   = 131
	oneHandedBlunt = 132
	staff          = 138
)

// weaponType is the equipped weapon's item category, 0 when bare-handed.
func weaponType(ch domain.Character) int {
	for _, e := range ch.Equipment {
		if e.Slot == -11 {
			return e.ItemID / 10000
		}
	}
	return 0
}

// learnedLevel is the character's live level of a skill, 0 when not learned.
func learnedLevel(ch domain.Character, id int, now time.Time) int {
	for _, s := range ch.Skills {
		if s.ID == id && s.Level > 0 && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

// skillValues is the WZ level values of another skill.
func skillValues(id, level int) (map[string]int, error) {
	def, err := content.ConfiguredSkillDefinition(id)
	if err != nil {
		return nil, err
	}
	return def.Values(min(max(level, 1), def.MaxLevel))
}

// activeRow returns the live buff row of skill id carrying stat.
func activeRow(ch domain.Character, id int, stat string, now time.Time) (domain.SkillBuff, bool) {
	for _, b := range ch.SkillBuffs {
		if b.ID == id && b.Stat == stat && b.ExpiresAt.After(now) {
			return b, true
		}
	}
	return domain.SkillBuff{}, false
}

func stat(name buffstat.Stat, v int) skills.StatValue { return skills.StatValue{Stat: name, Value: v} }

// cost is skills.StandardCost with the Demon Slayer resource: WZ forceCon is
// paid from MP (Java calcMPChange: demons pay forceCon instead of mpCon), and
// nothing while BOUNDLESS_RAGE is active.
func cost(c *skills.CastContext) skills.CastPlan {
	p := skills.StandardCost(c)
	if force := c.Value("forceCon"); force > 0 {
		p.MPCost = force
		if _, ok := skills.ActiveStat(c.Caster, "BOUNDLESS_RAGE", c.Now); ok {
			p.MPCost = 0
		}
	}
	return p
}

// buff is cost plus stats for the WZ time in seconds.
func buff(c *skills.CastContext, stats ...skills.StatValue) (skills.CastPlan, error) {
	p := cost(c)
	if c.Value("time") < 1 || len(stats) == 0 {
		return p, fmt.Errorf("skill%d: no buff duration", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: stats}
	return p, nil
}

// value casts a single Java statup taking a WZ key, e.g. BOOSTER = x.
func value(name buffstat.Stat, key string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		if c.Value(key) == 0 {
			return cost(c), fmt.Errorf("skill%d: no %s", c.Skill.ID, key)
		}
		return buff(c, stat(name, c.Value(key)))
	}
}

// party moves the self buff of cast to the party members inside the WZ
// lt/rb range (Java isPartyBuff).
func party(cast func(*skills.CastContext) (skills.CastPlan, error)) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p, e := cast(c)
		if e != nil {
			return p, e
		}
		return skills.PartyPlan(c, p.Self, p)
	}
}

// costOnly pays the cost and shows the cast (Teleport, field objects).
func costOnly(c *skills.CastContext) (skills.CastPlan, error) { return cost(c), nil }

// heroesWill is skills.HerosWill (32121008).
func heroesWill(c *skills.CastContext) (skills.CastPlan, error) { return skills.HerosWill(c) }

// stun is Java MonsterStatus.STUN = 1 with the level's prop and time.
var stun = []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1}}

// poison is Java isPoison for skills with dot: POISON ticking dot% for
// dotTime (Carrion Breath, Binding Darkness).
var poison = combat.MobStatusApply{Stat: combat.MobPoison, Poison: true}

// prepare chains Prepare steps.
func prepare(steps ...func(*combat.AttackContext) error) func(*combat.AttackContext) error {
	if len(steps) == 0 {
		return nil
	}
	return func(ctx *combat.AttackContext) error {
		for _, f := range steps {
			if err := f(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// liveRow reports a live buff row of one of ids carrying stat (no clock in
// combat.DamageContext, so ceilings compare with the wall clock).
func liveRow(ch domain.Character, stat string, now time.Time, ids ...int) (domain.SkillBuff, bool) {
	for _, b := range ch.SkillBuffs {
		if b.Stat != stat || !b.ExpiresAt.After(now) {
			continue
		}
		for _, id := range ids {
			if b.ID == id {
				return b, true
			}
		}
	}
	return domain.SkillBuff{}, false
}
