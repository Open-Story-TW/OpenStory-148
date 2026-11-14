package resist1

import (
	"errors"
	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

// Battle Mage auras. Each is a permanent party toggle in the WZ lt/rb
// (Java isPartyBuff, duration 2100000000). With the advanced passive learned
// Java applies the advanced skill's effect instead (applyBuffEffect redirects
// 32001003→32120000, 32111012→32110000, 32101003→32120001) while the cost
// stays the basic skill's. Java also sends AURA (bit 108) with the level;
// the v145 client does not read that bit, so only the aura stat is sent.
type aura struct {
	id, advanced int
	stat         buffstat.Stat
	// valueKey is the WZ key of the aura value; "" sends the effect level
	// (BLUE_AURA and YELLOW_AURA carry the level, the client derives the
	// effect from it).
	valueKey string
}

var auras = []aura{
	{id: 32001003, advanced: 32120000, stat: "DARK_AURA", valueKey: "x"}, // 黑色繩索 Dark Aura: damage x%
	{id: 32111012, advanced: 32110000, stat: "BLUE_AURA"},                // 藍色繩索 Blue Aura
	{id: 32101003, advanced: 32120001, stat: "YELLOW_AURA"},              // 黃色繩索 Yellow Aura (Legacy blocks it: client error 38)
}

// auraFamily are the skills whose buffs one aura cast replaces.
var auraFamily = []int{32001003, 32111012, 32101003, 32111005, 32110007, 32110008, 32110009}

func init() {
	for _, a := range auras {
		skills.Register(skills.Handler{ID: a.id, Kind: skills.KindPartyBuff, Cast: a.cast, DropOnEnter: notBattleMage})
	}
	// The advanced auras are passives that upgrade the cast above.
	skills.Register(skills.Handler{ID: 32120000, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus { // 進階黑色繩索
		return skills.Bonus{MATK: c.Values["madX"]}
	}})
	skills.Register(skills.Handler{ID: 32110000, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus { // 進階藍色繩索
		return skills.Bonus{ASR: c.Values["asrR"], TER: c.Values["terR"]}
	}})
	skills.Register(skills.Handler{ID: 32120001, Kind: skills.KindPassive | skills.KindMobStatus, Passive: func(c *skills.PassiveContext) skills.Bonus { // 進階黃色繩索
		return skills.Bonus{AvoidRate: c.Values["er"]}
	}})

	skills.Register(skills.Handler{ID: 32111005, Kind: skills.KindBuff, Cast: bodyBoost}) // 超級體 Body Boost
	// Body Boost variants of each aura (invisible; Java 60 s, blue 10 s).
	skills.Register(skills.Handler{ID: 32110007, Kind: skills.KindBuff, Cast: boostedAura("DARK_AURA", "x", 60), DropOnEnter: notBattleMage})
	skills.Register(skills.Handler{ID: 32110008, Kind: skills.KindBuff, Cast: boostedAura("BLUE_AURA", "", 10), DropOnEnter: notBattleMage})
	skills.Register(skills.Handler{ID: 32110009, Kind: skills.KindBuff, Cast: boostedAura("YELLOW_AURA", "", 60), DropOnEnter: notBattleMage})
}

// notBattleMage drops an aura from a holder entering a field unless the
// holder is a Battle Mage (Java MapleMap cancels AURA for jobs outside
// 3200-3212 on every map entry, login included).
func notBattleMage(h domain.Character) bool { return h.Job < 3200 || h.Job > 3212 }

func (a aura) cast(c *skills.CastContext) (skills.CastPlan, error) {
	p := cost(c)
	p.Toggle = true
	// Java cancels the recipient's other auras and Body Boost first.
	for _, id := range auraFamily {
		if id != a.id {
			p.Replace = append(p.Replace, id)
		}
	}
	level, values := c.Level, c.Values
	if lv := learnedLevel(c.Caster, a.advanced, c.Now); lv > 0 {
		v, err := skillValues(a.advanced, lv)
		if err != nil {
			return p, err
		}
		level, values = lv, v
	}
	v := level
	if a.valueKey != "" {
		v = values[a.valueKey]
	}
	if v < 1 {
		return p, errors.New("aura without value")
	}
	return skills.PartyPlan(c, &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{stat(a.stat, v)}}, p)
}

// bodyBoost (32111005) needs a running aura: BODY_BOOST level for 60 s, 10 s
// over Blue Aura (Java applyBuffEffect), and the running aura is cut to the
// same duration (Retime).
func bodyBoost(c *skills.CastContext) (skills.CastPlan, error) {
	d, running := 0, 0
	for _, a := range []struct{ id, d int }{{32001003, 60}, {32101003, 60}, {32111012, 10}} {
		if skills.ActiveBuff(c.Caster, a.id, c.Now) {
			d, running = a.d, a.id
			break
		}
	}
	if running == 0 {
		return cost(c), errors.New("body boost needs an aura")
	}
	p := cost(c)
	p.Self = &skills.Buff{Duration: seconds(d), Stats: []skills.StatValue{stat("BODY_BOOST", c.Level)}}
	p.Retime = []skills.SkillDuration{{SkillID: running, Duration: seconds(d)}}
	return p, nil
}

// boostedAura is one of the invisible Body Boost aura skills: BODY_BOOST
// level plus the aura stat for d seconds (Java 32110007–32110009).
func boostedAura(name buffstat.Stat, key string, d int) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		v := c.Level
		if key != "" {
			v = c.Value(key)
		}
		p := cost(c)
		p.Self = &skills.Buff{Duration: seconds(d), Stats: []skills.StatValue{stat("BODY_BOOST", c.Level), stat(name, v)}}
		return p, nil
	}
}
