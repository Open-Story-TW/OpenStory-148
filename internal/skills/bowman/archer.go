package bowman

import "maple-go-server/internal/skills"

// 弓箭手 (job 300).
func init() {
	skills.Register(skills.Handler{ID: 3000001, Kind: skills.KindPassive, Passive: criticalShot})
	skills.Register(skills.Handler{ID: 3000002, Kind: skills.KindPassive, Passive: archeryMastery})
	skills.Register(skills.Handler{ID: 3000006, Kind: skills.KindPassive})                       // PvP-only (Java PlayerStats.pvpSkills); no PvE effect
	skills.Register(skills.Handler{ID: 3001004, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Arrow Blow: 3 mobs, lt/rb (-120,-75)-(120,30)
	skills.Register(skills.Handler{ID: 3001005, Kind: skills.KindAttack, Attack: shoot(rule{})}) // Double Shot: bulletCount 2
}

// criticalShot (3000001 霸王箭) adds prop percent critical rate (Java
// CalcPassive_SharpEye).
func criticalShot(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{Critical: c.Values["prop"]}
}

// archeryMastery (3000002 百步穿楊) also raises the movement speed cap.
func archeryMastery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{
		ACC: c.Values["acc"], AVOID: c.Values["eva"], Speed: c.Values["speed"],
		Range: c.Values["range"], SpeedLimitBonus: c.Values["speedMax"],
	}
}
