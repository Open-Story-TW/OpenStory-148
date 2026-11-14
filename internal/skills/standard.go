package skills

import (
	"fmt"
	"maple-go-server/internal/buffstat"
	"time"
)

// standardStats is Java MapleStatEffect's generic WZ-key to buff-stat mapping.
var standardStats = []struct {
	key  string
	stat buffstat.Stat
}{
	{"pad", "WATK"}, {"pdd", "WDEF"}, {"mad", "MATK"}, {"mdd", "MDEF"},
	{"acc", "ACC"}, {"eva", "AVOID"}, {"speed", "SPEED"}, {"jump", "JUMP"},
	{"mhpR", "MAXHP"}, {"mmpR", "MAXMP"},
}

// StandardStats returns the generic stats present (non-zero) in the values.
func StandardStats(values map[string]int) []StatValue {
	var out []StatValue
	for _, s := range standardStats {
		if v := values[s.key]; v != 0 {
			out = append(out, StatValue{Stat: s.stat, Value: v})
		}
	}
	return out
}

// StandardCost fills MPCost, HPCost, Items and Cooldown from mpCon, hpCon,
// itemCon/itemConNo and cooltime.
func StandardCost(c *CastContext) CastPlan {
	// forceCon is the Demon Fury cost of Demon Slayer skills; Demon Fury is
	// the job's MP.
	p := CastPlan{MPCost: c.Value("mpCon") + c.Value("forceCon"), HPCost: c.Value("hpCon"), Cooldown: time.Duration(c.Value("cooltime")) * time.Second, MoveTo: c.Value("moveTo")}
	if id := c.Value("itemCon"); id > 0 {
		p.Items = []ItemCost{{ItemID: id, Quantity: max(1, c.Value("itemConNo"))}}
	}
	return p
}

// CostOnly is the Cast of a skill the client resolves alone (Double Jump,
// Teleport): it pays the standard cost and shows the cast.
func CostOnly(c *CastContext) (CastPlan, error) { return StandardCost(c), nil }

// HerosWill is the template of Hero's Will and Maple purification: standard
// cost and removal of the caster's abnormal statuses. Job threads register it
// for their own skill IDs.
func HerosWill(c *CastContext) (CastPlan, error) {
	p := StandardCost(c)
	p.Dispel = true
	return p, nil
}

// StandardBuff is the Cast of a plain self buff: standard cost and the
// generic WZ stats for time seconds.
func StandardBuff(c *CastContext) (CastPlan, error) {
	p := StandardCost(c)
	stats := StandardStats(c.Values)
	if len(stats) == 0 || c.Value("time") < 1 {
		return p, fmt.Errorf("skill%d: no standard buff", c.Skill.ID)
	}
	p.Self = &Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: stats}
	return p, nil
}

// StandardPartyBuff is StandardBuff applied to the party members inside the
// skill's WZ lt/rb range instead of the caster alone.
func StandardPartyBuff(c *CastContext) (CastPlan, error) {
	p, e := StandardBuff(c)
	if e != nil {
		return p, e
	}
	return PartyPlan(c, p.Self, p)
}

// PartyPlan turns buff into the party part of plan, inside the WZ range.
func PartyPlan(c *CastContext, buff *Buff, plan CastPlan) (CastPlan, error) {
	r, ok := c.Skill.RangeAt(c.Level)
	if !ok {
		return plan, fmt.Errorf("skill%d: party buff without lt/rb", c.Skill.ID)
	}
	plan.Self, plan.Party = nil, buff
	plan.Area = &Rect{Left: r.LT[0], Top: r.LT[1], Right: r.RB[0], Bottom: r.RB[1]}
	return plan, nil
}
