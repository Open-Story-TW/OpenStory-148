package warrior

import (
	"errors"
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"time"
)

// melee is the attack spec of every warrior attack and final attack: all of
// them use the close-range attack packet (WZ info type 1 / 53). onHit are
// the Java MapleStatEffect monsterStatus entries, rolled with the level's
// prop for its time; prepare steps run in order before planning.
func melee(onHit []combat.MobStatusApply, prepare ...func(*combat.AttackContext) error) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: combat.Melee, OnHit: onHit}
	if len(prepare) > 0 {
		s.Prepare = func(ctx *combat.AttackContext) error {
			for _, f := range prepare {
				if err := f(ctx); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return s
}

// stun is Java MonsterStatus.STUN = 1.
var stun = []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1}}

// learnedLevel is the character's live level of a skill, 0 when not learned.
func learnedLevel(ch domain.Character, id int) int {
	for _, s := range ch.Skills {
		if s.ID == id {
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
	return def.Values(min(level, def.MaxLevel))
}

// activeBuff returns the first of ids the character has a live buff from, 0
// when none.
func activeBuff(ch domain.Character, now time.Time, ids ...int) int {
	for _, b := range ch.SkillBuffs {
		if b.ExpiresAt.After(now) {
			for _, id := range ids {
				if b.ID == id {
					return id
				}
			}
		}
	}
	return 0
}

// Element charges (Java DamageParse: the WK_CHARGE source sets the element
// of every attack while it lasts).
var chargeElements = map[int]combat.Element{1211004: combat.Fire, 1211006: combat.Ice, 1211008: combat.Lightning, 1221004: combat.Holy}

// charged gives the attack the element of the caster's active charge.
func charged(ctx *combat.AttackContext) error {
	if id := activeBuff(ctx.Character, ctx.Now, 1211004, 1211006, 1211008, 1221004); id != 0 {
		ctx.Effect.Element = chargeElements[id]
	}
	return nil
}

// holy is the WZ elemAttr "h" of Heaven's Hammer.
func holy(ctx *combat.AttackContext) error {
	ctx.Effect.Element = combat.Holy
	return nil
}

// hpRatio pays x% of the current HP (Java hpR = -x/100: Sacrifice, Dragon
// Roar).
func hpRatio(ctx *combat.AttackContext) error {
	ctx.Effect.HPRatio = ctx.Effect.X
	return nil
}

// ignoreDefense is WZ info ignoreTargetPDP (Sacrifice).
func ignoreDefense(ctx *combat.AttackContext) error {
	ctx.Effect.IgnoreMobDefense = 100
	return nil
}

// finisher requires the v148 WZ y combo orbs. The damage ceiling scales by
// the current orb count, and Combo Attack consumes the required orbs.
func finisher(ctx *combat.AttackContext) error {
	value, ok := skills.ActiveStat(ctx.Character, "COMBO", ctx.Now)
	if !ok || ctx.Effect.Y < 1 || value-1 < ctx.Effect.Y {
		return errors.New("finisher needs combo orbs")
	}
	ctx.Effect.Damage *= value - 1
	return nil
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

// partyBuff is buff applied to the party members inside the skill's WZ lt/rb
// (Java isPartyBuff).
func partyBuff(c *skills.CastContext, stats ...skills.StatValue) (skills.CastPlan, error) {
	p, err := buff(c, stats...)
	if err != nil {
		return p, err
	}
	return skills.PartyPlan(c, p.Self, p)
}

func stat(name buffstat.Stat, v int) skills.StatValue { return skills.StatValue{Stat: name, Value: v} }

// value casts a single Java statup taking a WZ key, e.g. BOOSTER = x.
func value(name buffstat.Stat, key string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) { return buff(c, stat(name, c.Value(key))) }
}

// physicalTraining is Java localstr/localdex += strX/dexX (1x00009).
func physicalTraining(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
}

// weaponType is the equipped weapon's item category (itemID / 10000), 0 when
// bare-handed.
func weaponType(ch domain.Character) int {
	for _, e := range ch.Equipment {
		if e.Slot == -11 {
			return e.ItemID / 10000
		}
	}
	return 0
}

// Warrior weapon categories.
const (
	oneHandedSword = 130
	oneHandedAxe   = 131
	oneHandedBlunt = 132
	twoHandedSword = 140
	twoHandedAxe   = 141
	twoHandedBlunt = 142
	spear          = 143
	polearm        = 144
)

// mastery is a weapon mastery passive (Java PlayerStats.calculateMasteryValue):
// mastery and x accuracy while one of the weapons is equipped.
func mastery(weapons ...int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		w := weaponType(c.Character)
		for _, ok := range weapons {
			if w == ok {
				return skills.Bonus{Mastery: c.Values["mastery"], ACC: c.Values["x"]}
			}
		}
		return skills.Bonus{}
	}
}

// magicCrash is Java removeMonsterBuff for 1111007/1211009/1311007: inside
// lt/rb, up to mobCount monsters lose their attack/defence ups at prop%, and
// get MAGIC_CRASH for time (no new buffs).
func magicCrash() *combat.AttackSpec {
	return &combat.AttackSpec{MobCast: &combat.MobCastSpec{Dispel: true, UseBox: true, Statuses: []combat.MobStatusApply{{Stat: combat.MobMagicCrash, Value: 1}}}}
}
