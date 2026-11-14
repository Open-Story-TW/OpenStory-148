package aranevan

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"slices"
	"time"
)

// learned is the live level of a learned skill, 0 when not learned.
func learned(ch domain.Character, id int, now time.Time) int {
	for _, s := range ch.Skills {
		if s.ID == id && (s.ExpiresAt.IsZero() || s.ExpiresAt.After(now)) {
			return s.Level
		}
	}
	return 0
}

// values are the WZ values of a skill level.
func values(id, level int) (map[string]int, error) {
	def, err := content.SkillDefinition("", id)
	if err != nil {
		return nil, err
	}
	return def.Values(min(level, def.MaxLevel))
}

// learnedValue is a WZ key of another learned skill, 0 when not learned.
func learnedValue(ch domain.Character, id int, key string, now time.Time) int {
	lv := learned(ch, id, now)
	if lv < 1 {
		return 0
	}
	v, err := values(id, lv)
	if err != nil {
		return 0
	}
	return v[key]
}

// activeRow returns the caster's live buff row of skill id with stat.
func activeRow(ch domain.Character, id int, stat buffstat.Stat, now time.Time) (domain.SkillBuff, bool) {
	for _, b := range ch.SkillBuffs {
		if b.ID == id && b.Stat == string(stat) && b.ExpiresAt.After(now) {
			return b, true
		}
	}
	return domain.SkillBuff{}, false
}

// activeBuff reports a live buff from any of ids.
func activeBuff(ch domain.Character, now time.Time, ids ...int) bool {
	for _, b := range ch.SkillBuffs {
		if b.ExpiresAt.After(now) && slices.Contains(ids, b.ID) {
			return true
		}
	}
	return false
}

// weaponType is the equipped weapon's item id / 10000, 0 when bare-handed.
func weaponType(ch domain.Character) int {
	for _, e := range ch.Equipment {
		if e.Slot == -11 {
			return e.ItemID / 10000
		}
	}
	return 0
}

func stat(name buffstat.Stat, v int) skills.StatValue { return skills.StatValue{Stat: name, Value: v} }

// enhancedStats is Java MapleStatEffect's generic epad/epdd/emdd statups.
func enhancedStats(c *skills.CastContext) []skills.StatValue {
	var out []skills.StatValue
	for _, s := range []struct {
		key  string
		stat buffstat.Stat
	}{{"epad", "ENHANCED_WATK"}, {"epdd", "ENHANCED_WDEF"}, {"emdd", "ENHANCED_MDEF"}} {
		if v := c.Value(s.key); v != 0 {
			out = append(out, stat(s.stat, v))
		}
	}
	return out
}

// buff is the standard cost plus stats for the WZ time in seconds (Java
// MapleStatEffect statups of an overTime effect).
func buff(c *skills.CastContext, stats ...skills.StatValue) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("time") < 1 || len(stats) == 0 {
		return p, fmt.Errorf("skill%d: no buff duration", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: stats}
	return p, nil
}

// value casts a Java statup taking a WZ key, followed by the generic WZ
// stats (pdd, mdd, mhpR...) Java adds to every overTime effect.
func value(name buffstat.Stat, key string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		if c.Value(key) == 0 {
			return skills.CastPlan{}, fmt.Errorf("skill%d: no %s", c.Skill.ID, key)
		}
		return buff(c, append([]skills.StatValue{stat(name, c.Value(key))}, skills.StandardStats(c.Values)...)...)
	}
}

// party moves the self buff of cast to the party members inside the WZ lt/rb
// (Java isPartyBuff).
func party(cast func(*skills.CastContext) (skills.CastPlan, error)) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p, e := cast(c)
		if e != nil {
			return p, e
		}
		return skills.PartyPlan(c, p.Self, p)
	}
}

// steps chains Prepare functions in order.
func steps(fs ...func(*combat.AttackContext) error) func(*combat.AttackContext) error {
	if len(fs) == 0 {
		return nil
	}
	return func(ctx *combat.AttackContext) error {
		for _, f := range fs {
			if err := f(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// poison is Java isPoison (dot and dotTime): POISON for max(time, dotTime).
func poison(ctx *combat.AttackContext) error {
	if ctx.Effect.DOT > 0 && ctx.Effect.DOTTime > 0 {
		ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobPoison, Value: 1, Poison: true, Duration: max(ctx.Effect.Time, ctx.Effect.DOTTime)})
	}
	return nil
}

// freeze is Java's FREEZE with the doubled duration of freezing skills.
func freeze(ctx *combat.AttackContext) error {
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobFreeze, Value: 1, Duration: 2 * ctx.Effect.Time})
	return nil
}

// stun is Java MonsterStatus.STUN = 1 for the WZ time at prop.
var stun = []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1}}

// box is a skill's WZ lt/rb as a combat box.
func box(id int) combat.Box {
	def, err := content.SkillDefinition("", id)
	if err != nil {
		panic(err)
	}
	r, ok := def.RangeAt(1)
	if !ok {
		panic(fmt.Sprintf("skill%d: no lt/rb", id))
	}
	return combat.Box{LT: combat.Point{X: r.LT[0], Y: r.LT[1]}, RB: combat.Point{X: r.RB[0], Y: r.RB[1]}, Set: true}
}
