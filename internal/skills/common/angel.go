package common

import (
	"fmt"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Original v148 0x72E0C7 derives 86/88/91/180 from 1085/1087/1090/1179,
// sends the owned summon and hidden skill through 0x122, then waits time seconds.
func angelSub(c combat.SubSummonContext) (combat.SubSummonResult, error) {
	if c.SubSkill != c.SummonSkill-999 || c.SubLevel != 1 || skills.EquipmentRelatedSkillLevel(c.Character, c.SubSkill, c.Now) != 1 {
		return combat.SubSummonResult{}, fmt.Errorf("unowned angel related skill %d", c.SubSkill)
	}
	interval := time.Duration(c.SubValues["time"]) * time.Second
	if interval <= 0 || !c.LastSub.IsZero() && c.Now.Before(c.LastSub.Add(interval)) {
		return combat.SubSummonResult{}, fmt.Errorf("angel related skill not ready")
	}
	if c.SubValues["indiePad"] <= 0 || c.SubValues["indieMad"] <= 0 {
		return combat.SubSummonResult{}, fmt.Errorf("angel related effect missing")
	}
	owner, skill := c.Character.ID, c.SubSkill
	b := &skills.Buff{Duration: interval, Stats: []skills.StatValue{
		{Stat: "ANGEL_ATK", Value: c.SubValues["indiePad"]},
		{Stat: "ANGEL_MATK", Value: c.SubValues["indieMad"]},
	}}
	return combat.SubSummonResult{Action: 8, Apply: func() {
		skills.ApplyBuff(owner, skill, 1, skills.BuffChange{Buff: b})
	}}, nil
}
