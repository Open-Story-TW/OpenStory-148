package pirate

import "maple-go-server/internal/skills"

func init() {
	for _, row := range []struct {
		id      int
		passive func(*skills.PassiveContext) skills.Bonus
	}{
		{5080000, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{ACC: c.Values["accX"], Jump: c.Values["psdJump"], SpeedLimitBonus: c.Values["speedMax"]}
		}},
		{5080004, critical("cr")},
		{5090000, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{ACC: c.Values["accX"], AVOID: c.Values["evaX"]}
		}},
		{5700000, mastery(gun, "x")},
		{5700003, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
		}},
		{5710004, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{
				MaxHP: c.Values["mhpX"], MaxMP: c.Values["mmpX"], ASR: c.Values["asrR"],
				WDEFPercent: c.Values["pddR"], MDEFPercent: c.Values["mddR"],
			}
		}},
		{5710005, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{Critical: c.Values["cr"], IgnoreDefense: c.Values["ignoreMobpdpR"]}
		}},
		{5720008, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{Critical: c.Values["cr"], CriticalDamageMin: c.Values["criticaldamageMin"],
				CriticalDamage: c.Values["criticaldamageMax"], BossCritical: c.Values["prop"], BossDamagePercent: c.Values["bdR"]}
		}},
		{5800001, mastery(knuckle, "x")},
		{5800008, critical("cr")},
		{5800009, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{MaxHPPercent: c.Values["mhpR"]}
		}},
		{5810000, stunnedCritical},
		{5810008, func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{SkillDamage: skillDamage(5801002, c.Values["x"], 5801003, c.Values["y"], 5801004, c.Values["z"])}
		}},
		{5900000, mastery(gun, "x")},
		{5900007, critical("cr")},
	} {
		skills.Register(skills.Handler{ID: row.id, Kind: skills.KindPassive, Passive: row.passive})
	}
	// WZ info type 40 runs this forward somersault on the native movement path.
	skills.Register(skills.Handler{ID: 5081003, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	skills.Register(skills.Handler{ID: 5801006, Kind: skills.KindBuff, Cast: valueBuff("BOOSTER", "x")})
	skills.Register(skills.Handler{ID: 5811005, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1000, false)})
	skills.Register(skills.Handler{ID: 5821003, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1001, false)})
	skills.Register(skills.Handler{ID: 5821009, Kind: skills.KindPartyBuff, Cast: party(valueBuff("SPEED_INFUSION", "x"))})
	// 0x8F40C6 requests the standard 0xA3 cost, then sets the glide on the native mover.
	skills.Register(skills.Handler{ID: 5901005, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	// Both regional dice cast through the native 0xA3 sender. The upgrade
	// has its own WZ prop, duration, cooldown and prerequisite.
	skills.Register(skills.Handler{ID: 5711011, Kind: skills.KindBuff, Cast: dice(6)})
	skills.Register(skills.Handler{ID: 5720005, Kind: skills.KindBuff, Cast: doubleDice})
	skills.Register(skills.Handler{ID: 5911007, Kind: skills.KindBuff, Cast: dice(6)})
}
