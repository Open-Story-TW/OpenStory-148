package warrior

import "maple-go-server/internal/skills"

func init() {
	skills.Register(skills.Handler{ID: 1000006, Kind: skills.KindPassive, Passive: hpIncrease})
	skills.Register(skills.Handler{ID: 1000007, Kind: skills.KindPassive})                         // Guardian Gloves: PvP only (Java PlayerStats.pvpSkills), no PvE effect
	skills.Register(skills.Handler{ID: 1001003, Kind: skills.KindBuff, Cast: skills.StandardBuff}) // Iron Body: pdd
	skills.Register(skills.Handler{ID: 1001004, Kind: skills.KindAttack, Attack: melee(nil)})      // Power Strike
	skills.Register(skills.Handler{ID: 1001005, Kind: skills.KindAttack, Attack: melee(nil)})      // Slash Blast
}

// hpIncrease (1000006) is Java percent_hp += mhpR for every warrior job.
// content/vitals.go applies it until the stats provider sums passives.
func hpIncrease(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{MaxHPPercent: c.Values["mhpR"]}
}
