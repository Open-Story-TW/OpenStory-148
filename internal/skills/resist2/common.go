package resist2

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

// Weapon types (item id / 10000) the mastery passives require.
const (
	crossbow = 146
	gun      = 149
)

// SummonMovementType values (Java MapleStatEffect.getSummonMovementType).
const (
	stationary     = 0
	follow         = 1
	walkStationary = 2
	circleFollow   = 4
)

// weaponType is the equipped weapon's item id / 10000, 0 when bare-handed.
func weaponType(c domain.Character) int {
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

// skillValues returns the WZ values of skill id at level (clamped to its
// maximum), nil when the level is 0 or the skill is unknown.
func skillValues(id, level int) map[string]int {
	if level < 1 {
		return nil
	}
	def, err := content.SkillDefinition("", id)
	if err != nil {
		return nil
	}
	v, err := def.Values(min(level, def.MaxLevel))
	if err != nil {
		return nil
	}
	return v
}

// buffSource returns the skill whose active row carries stat, 0 when none.
func buffSource(c domain.Character, stat buffstat.Stat, now time.Time) int {
	for _, b := range c.SkillBuffs {
		if b.Stat == string(stat) && b.ExpiresAt.After(now) {
			return b.ID
		}
	}
	return 0
}

// buffTime is the WZ time in seconds.
func buffTime(c *skills.CastContext) (time.Duration, error) {
	if c.Value("time") < 1 {
		return 0, fmt.Errorf("skill%d: no time", c.Skill.ID)
	}
	return time.Duration(c.Value("time")) * time.Second, nil
}

// stat is one buff stat and the WZ key of its value.
type stat struct {
	name buffstat.Stat
	key  string
}

// buff is the Cast of a self buff of the given stats for the WZ time.
func buff(stats ...stat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d, e := buffTime(c)
		if e != nil {
			return p, e
		}
		b := &skills.Buff{Duration: d}
		for _, s := range stats {
			if c.Value(s.key) == 0 {
				return p, fmt.Errorf("skill%d: no %s", c.Skill.ID, s.key)
			}
			b.Stats = append(b.Stats, skills.StatValue{Stat: s.name, Value: c.Value(s.key)})
		}
		p.Self = b
		return p, nil
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

// sharpEyes (33121004) packs the critical rate x in the high byte and the
// critical damage y in the low byte, like the bowman Sharp Eyes (Java reads
// criticaldamageMax, which the v145 WZ does not have).
func sharpEyes(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	d, e := buffTime(c)
	if e != nil || c.Value("x") < 1 {
		return p, fmt.Errorf("skill%d: invalid values", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: "SHARP_EYES", Value: c.Value("x")<<8 | c.Value("y")&0xff}}}
	return p, nil
}

// mount is a riding skill (Java isMonsterRiding): MONSTER_RIDING carries the
// mount item, as Java giveMount sends it, plus extra stats, for
// skills.Permanent. Recasting dismounts.
func mount(c *skills.CastContext, item int, extra []skills.StatValue) skills.CastPlan {
	p := skills.StandardCost(c)
	p.Self = &skills.Buff{Duration: skills.Permanent, Stats: append([]skills.StatValue{{Stat: "MONSTER_RIDING", Value: item}}, extra...)}
	p.Toggle = true
	return p
}

// enhanced maps the WZ e* and indie keys of a mount to buff stats (Java
// MapleStatEffect generic statups).
var enhanced = []stat{
	{"ENHANCED_WATK", "epad"}, {"ENHANCED_WDEF", "epdd"}, {"ENHANCED_MDEF", "emdd"},
	{"ENHANCED_MAXHP", "emhp"}, {"ENHANCED_MAXMP", "emmp"}, {"ANGEL_SPEED", "indieSpeed"},
}

func enhancedStats(v map[string]int) []skills.StatValue {
	var out []skills.StatValue
	for _, s := range enhanced {
		if x := v[s.key]; x != 0 {
			out = append(out, skills.StatValue{Stat: s.name, Value: x})
		}
	}
	return out
}

// rollDie returns 1..faces; tests replace it.
var rollDie = func(faces int) int { return rand.IntN(faces) + 1 }

// dice (35111013) is Lucky Dice: a 6-face roll is the DICE_ROLL value and
// the client derives the effect from the WZ values. A 1 has no buff but MP and
// cooldown are still spent. Java Legacy hard-codes the roll to 5 (a debug
// override, same as the pirate dice).
func dice(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	d, e := buffTime(c)
	if e != nil {
		return p, e
	}
	if roll := rollDie(6); roll > 1 {
		p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: "DICE_ROLL", Value: roll}}}
	}
	return p, nil
}

// summonBuff is the summon's buff: stat 1 for the WZ time, or for
// skills.Permanent when permanent (Java duration 2100000000). Robot
// Mastery (35120001) lengthens mechanic summons by y percent (Java
// BuffUP_Summon).
func summonBuff(st buffstat.Stat, permanent bool) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d := skills.Permanent
		if !permanent {
			var e error
			if d, e = buffTime(c); e != nil {
				return p, e
			}
			if c.Skill.ID/10000 >= 3510 {
				if v := skillValues(robotMastery, learned(c.Caster, robotMastery)); v != nil {
					d = d * time.Duration(100+v["y"]) / 100
				}
			}
		}
		p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: st, Value: 1}}}
		return p, nil
	}
}

// stun is Java's STUN 1 for the WZ time at the WZ prop.
var stun = []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1}}

// skillDamage packs (skill, percent) pairs into Bonus.SkillDamage.
func skillDamage(pairs ...int) (out [4]skills.SkillDamage) {
	for i := 0; i+1 < len(pairs); i += 2 {
		out[i/2] = skills.SkillDamage{Skill: pairs[i], Percent: pairs[i+1]}
	}
	return out
}

// passive registers an always-on bonus.
func passive(id int, f func(*skills.PassiveContext) skills.Bonus) {
	skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
}

// mastery is Java CalcPassive_Mastery: with the matching weapon, the skill's
// mastery, x accuracy and cr critical rate.
func mastery(weapon int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		if weaponType(c.Character) != weapon {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.Values["mastery"], ACC: c.Values["x"], Critical: c.Values["cr"]}
	}
}

// physicalTraining (33100010, 35100011) adds strX STR and dexX DEX.
func physicalTraining(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
}
