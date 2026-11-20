package pirate

import (
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"time"
)

// energyFull is the Energy Charge gauge maximum (Java handleEnergyCharge).
const energyFull = 10000

func init() {
	skills.Register(skills.Handler{ID: 5110001, Kind: skills.KindPassive | skills.KindBuff, OnAttack: energyCharge}) // 蓄能激發 Energy Charge
	skills.Register(skills.Handler{ID: 5810001, Kind: skills.KindPassive | skills.KindBuff, OnAttack: regionalEnergyCharge})
}

// energyCharge follows Java MapleCharacter.handleEnergyCharge for Marauders
// and Buccaneers (511, 512): the first attack starts an untimed gauge at 0,
// each later attack adds x per target line until 10000, and the attack after the
// gauge is full starts the timed full charge (WZ time). While the timed
// charge runs the gauge does not change; when it ends the next attack starts
// over.
func energyCharge(c *skills.TriggerContext) *skills.BuffChange {
	if j := c.Character.Job; j != 511 && j != 512 {
		return nil
	}
	gain := c.Value("x")
	// Energy Explosion grants its own WZ x on hits while charging.
	if level := skills.EffectiveLevel(c.Character, 5121016, c.Now); level > 0 {
		if def, err := content.ConfiguredSkillDefinition(5121016); err == nil {
			if values, err := def.Values(min(level, def.MaxLevel)); err == nil {
				gain += values["x"]
			}
		}
	}
	return chargeEnergyGauge(c, gain)
}

// v148's native gauge UI (0x918D29) selects 5810001 for the regional
// knuckle line. Its charge gain and duration come from its own WZ levels.
func regionalEnergyCharge(c *skills.TriggerContext) *skills.BuffChange {
	if j := c.Character.Job; j != 581 && j != 582 {
		return nil
	}
	return chargeEnergyGauge(c, c.Value("x"))
}

func chargeEnergyGauge(c *skills.TriggerContext, gain int) *skills.BuffChange {
	if c.Attack == nil || c.Attack.Summon || len(c.Attack.Targets) == 0 {
		return nil
	}
	gauge := func(v int, d time.Duration) *skills.BuffChange {
		return &skills.BuffChange{Buff: &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: "ENERGY_CHARGE", Value: v}}}}
	}
	v, ok := c.Stat("ENERGY_CHARGE")
	if !ok {
		return gauge(0, skills.Permanent)
	}
	timed := false
	for _, r := range c.Rows {
		if r.Stat == "ENERGY_CHARGE" && r.ExpiresAt.Sub(c.Now) <= time.Duration(c.Value("time"))*time.Second {
			timed = true
		}
	}
	switch {
	case timed:
		return nil
	case v < energyFull:
		return gauge(min(energyFull, v+gain*len(c.Attack.Targets)*max(1, c.Attack.Lines)), skills.Permanent)
	case c.Value("time") > 0:
		return gauge(energyFull, time.Duration(c.Value("time"))*time.Second)
	}
	return nil
}
