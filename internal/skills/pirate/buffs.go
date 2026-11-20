package pirate

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"os"
	"time"
)

func init() {
	for _, id := range []int{5101006, 5201003, 5301002, 5701005, 5901003} { // 致命快打／迅雷再起／加農砲推進器 Booster
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: valueBuff("BOOSTER", "x")})
	}
	for _, id := range []int{5121000, 5221000, 5321005, 5721000, 5821000, 5921000} { // 楓葉祝福 Maple Warrior
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPartyBuff, Cast: party(valueBuff("MAPLE_WARRIOR", "x"))})
	}
	for _, id := range []int{5120011, 5220012, 5720012, 5820011, 5920012} { // 反擊 Pirate's Revenge
		// The original case2 handler has an affected animation for these WZ entries.
		affected := id == 5120011 || id == 5220012 || id == 5720012
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, OnDamaged: piratesRevenge,
			Passive: piratesRevengeDamage, TriggerAffectedEffect: affected})
	}
	for _, pair := range [][2]int{{5111007, 5120012}, {5211007, 5220014}} {
		skills.Register(skills.Handler{ID: pair[0], Kind: skills.KindBuff, Cast: luckyDice(pair[1])})
		// The client substitutes the learned passive's ID when casting Lucky Dice.
		skills.Register(skills.Handler{ID: pair[1], Kind: skills.KindPassive, Cast: upgradedLuckyDice(pair[0])})
	}
	skills.Register(skills.Handler{ID: 5311005, Kind: skills.KindBuff, Cast: dice(6)})
	skills.Register(skills.Handler{ID: 5311004, Kind: skills.KindBuff, Cast: dice(4)})    // 幸運木桶 Barrel Roulette
	skills.Register(skills.Handler{ID: 5320007, Kind: skills.KindBuff, Cast: doubleDice}) // 雙倍幸運骰子 Double Lucky Dice
	for _, id := range []int{5001005, 5091005} {                                          // 衝鋒 Dash; both v148 skills are dispatched by the movement keys.
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: dash})
	}
	skills.Register(skills.Handler{ID: 5121009, Kind: skills.KindPartyBuff, Cast: party(valueBuff("SPEED_INFUSION", "x"))}) // 最終極速 Speed Infusion
	skills.Register(skills.Handler{ID: 5321010, Kind: skills.KindBuff, Cast: valueBuff("STANCE", "prop")})                  // 百烈精神 Pirate's Spirit
	skills.Register(skills.Handler{ID: 5101007, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1002, false)})        // 偽裝術 Oak Barrel
	skills.Register(skills.Handler{ID: 5111005, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1000, true)})         // 鬥神附體 Transformation
	skills.Register(skills.Handler{ID: 5121003, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1001, true)})         // 鬥神降世 Super Transformation
	for _, id := range []int{5301003, 5320008} {                                                                            // 猴子的魔法／神聖猴子的咒語 Monkey Magic
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPartyBuff, Cast: party(indieBuff)})
	}
}

// buffTime is the WZ time in seconds.
func buffTime(c *skills.CastContext) (time.Duration, error) {
	if c.Value("time") < 1 {
		return 0, fmt.Errorf("skill%d: no time", c.Skill.ID)
	}
	return time.Duration(c.Value("time")) * time.Second, nil
}

// valueBuff is a self buff of one stat whose value is the WZ key (Java
// MapleStatEffect: BOOSTER x, MAPLE_WARRIOR x, SPEED_INFUSION x, STANCE prop).
func valueBuff(stat buffstat.Stat, key string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d, e := buffTime(c)
		if e != nil {
			return p, e
		}
		if c.Value(key) == 0 {
			return p, fmt.Errorf("skill%d: no %s", c.Skill.ID, key)
		}
		p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: stat, Value: c.Value(key)}}}
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

// Pirate's Revenge rolls prop on damage taken and keeps the WZ time and
// x-second lockout. v148's WZ indieDamR supplies the magnitude. The original
// damage calculators read the general additive stat at 0x713637 and 0x708432
// (SecondaryStat bit 116). Bit 162 requires the Arcane Aim job context.
func piratesRevenge(c *skills.TriggerContext) *skills.BuffChange {
	damage := c.Value("damR")
	if value, ok := c.Values["indieDamR"]; ok {
		damage = value
	}
	if c.Character.Job != c.Skill.ID/10000 || c.Damage <= 0 || len(c.Rows) > 0 || c.Value("time") < 1 || damage <= 0 {
		return nil
	}
	for _, cd := range c.Character.SkillCooldowns {
		if cd.ID == c.Skill.ID && cd.ExpiresAt.After(c.Now) {
			return nil
		}
	}
	if prop := c.Value("prop"); prop < 100 && rollDie(100) > prop {
		return nil
	}
	return &skills.BuffChange{
		Buff:     &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: []skills.StatValue{{Stat: "PIRATES_REVENGE", Value: damage}}},
		Cooldown: time.Duration(c.Value("x")) * time.Second,
	}
}

func piratesRevengeDamage(c *skills.PassiveContext) skills.Bonus {
	if c.Character.Job != c.Skill.ID/10000 {
		return skills.Bonus{}
	}
	for _, row := range c.Character.SkillBuffs {
		if row.ID == c.Skill.ID && row.Stat == "PIRATES_REVENGE" && row.ExpiresAt.After(c.Now) {
			return skills.Bonus{DamagePercent: row.Value}
		}
	}
	return skills.Bonus{}
}

// Dash uses the movement two-state stats for the source skill's speed x and jump y.
func dash(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	d, e := buffTime(c)
	if e != nil || c.Value("x") < 1 || c.Value("y") < 1 {
		return p, fmt.Errorf("skill%d: invalid dash values", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: "DASH_SPEED", Value: c.Value("x")}, {Stat: "DASH_JUMP", Value: c.Value("y")}}}
	return p, nil
}

// morph is a pirate transformation. Java getMorph fixes the morph id; the
// two Transformations (isPirateMorph) add STANCE 100 first, then the WZ
// epad/epdd/emdd enhanced stats, speed and jump.
func morph(id int, pirateMorph bool) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d, e := buffTime(c)
		if e != nil {
			return p, e
		}
		var stats []skills.StatValue
		if pirateMorph {
			stats = append(stats, skills.StatValue{Stat: "STANCE", Value: 100})
		}
		stats = append(stats, skills.StatValue{Stat: "MORPH", Value: id})
		for _, s := range []struct {
			key  string
			stat buffstat.Stat
		}{{"epad", "ENHANCED_WATK"}, {"epdd", "ENHANCED_WDEF"}, {"emdd", "ENHANCED_MDEF"}, {"speed", "SPEED"}, {"jump", "JUMP"}} {
			if v := c.Value(s.key); v != 0 {
				stats = append(stats, skills.StatValue{Stat: s.stat, Value: v})
			}
		}
		p.Self = &skills.Buff{Duration: d, Stats: stats}
		return p, nil
	}
}

// indieStats is Java MapleStatEffect's indie* key to buff-stat mapping.
var indieStats = []struct {
	key  string
	stat buffstat.Stat
}{
	{"indieMhp", "HP_BOOST"}, {"indieMmp", "MP_BOOST"}, {"indieJump", "ANGEL_JUMP"}, {"indieSpeed", "ANGEL_SPEED"},
	{"indieAcc", "ANGEL_ACC"}, {"indieEva", "ANGEL_AVOID"}, {"indieAllStat", "ANGEL_STAT"},
}

// indieBuff (5301003, 5320008) applies the WZ indie* stats.
func indieBuff(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	d, e := buffTime(c)
	if e != nil {
		return p, e
	}
	var stats []skills.StatValue
	for _, s := range indieStats {
		if v := c.Value(s.key); v != 0 {
			stats = append(stats, skills.StatValue{Stat: s.stat, Value: v})
		}
	}
	if len(stats) == 0 {
		return p, fmt.Errorf("skill%d: no indie stats", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: d, Stats: stats}
	return p, nil
}

// rollDie returns 1..faces; tests replace it.
var rollDie = func(faces int) int { return rand.IntN(faces) + 1 }

// dice is Lucky Dice (6 faces) and Barrel Roulette (4 faces). The roll is the
// DICE_ROLL value; the client derives the effect from it and the WZ u..z,
// damR, cr, pddR, mhpR/mmpR and expR values. A 1 has no effect (Java returns
// before the buff) but MP and cooldown are still spent.
//
// Java Legacy hard-codes the roll to 5 and the time to 90 seconds; both are
// debug overrides, so the WZ time and a real roll are used here.
func dice(faces int) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		return skills.DiceCast(c, rollDie(faces), 0, false)
	}
}

func luckyDice(upgradeID int) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		if diceCoolingDown(c, upgradeID) {
			return skills.CastPlan{}, fmt.Errorf("skill%d: Lucky Dice is cooling down", c.Skill.ID)
		}
		level := skills.EffectiveLevel(c.Caster, upgradeID, c.Now)
		if level == 0 || c.Level < 20 {
			return dice(6)(c)
		}
		upgrade, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), upgradeID)
		if err != nil {
			return skills.CastPlan{}, err
		}
		values, err := upgrade.Values(level)
		if err != nil {
			return skills.CastPlan{}, err
		}
		plan, err := doubleDice(&skills.CastContext{Skill: upgrade, Level: level, Values: values, Caster: c.Caster, Now: c.Now})
		plan.ApplyAs, plan.ApplyLevel = upgradeID, level
		return plan, err
	}
}

func upgradedLuckyDice(baseID int) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		if skills.EffectiveLevel(c.Caster, baseID, c.Now) < 20 || diceCoolingDown(c, baseID) {
			return skills.CastPlan{}, fmt.Errorf("skill%d: Lucky Dice is unavailable", baseID)
		}
		return doubleDice(c)
	}
}

func diceCoolingDown(c *skills.CastContext, id int) bool {
	for _, row := range c.Caster.SkillCooldowns {
		if row.ID == id && row.ExpiresAt.After(c.Now) {
			return true
		}
	}
	return false
}

// doubleDice (5320007) rolls one die and, with prop percent, a second; the
// DICE_ROLL value is d*100 for a double, d1*10+d2 for two dice, else the
// single die (Java MapleStatEffect case 5320007).
func doubleDice(c *skills.CastContext) (skills.CastPlan, error) {
	first, second := rollDie(6), 0
	if rollDie(100) <= c.Value("prop") {
		second = rollDie(6)
	}
	return skills.DiceCast(c, first, second, true)
}
