package thief

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Thief (400). Java Legacy MapleStatEffect, PlayerStats, PlayerHandler and
// DamageParse are the reference.
func init() {
	skills.Register(skills.Handler{ID: 4000000, Kind: skills.KindPassive, Passive: nimbleBody})
	skills.Register(skills.Handler{ID: 4000001, Kind: skills.KindPassive, Passive: keenEyes})
	skills.Register(skills.Handler{ID: 4000010, Kind: skills.KindPassive})                                                                                                                                                                   // Magic Wand: PvP-only (PlayerStats.pvpSkills)
	skills.Register(skills.Handler{ID: 4001002, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(combat.MobStatusApply{Stat: combat.MobPAD, ValueFrom: "x"}, combat.MobStatusApply{Stat: combat.MobPDR, ValueFrom: "y"}))}) // Disorder
	skills.Register(skills.Handler{ID: 4001003, Kind: skills.KindBuff, Cast: darkSight, OnAttack: darkSightBreak})
	skills.Register(skills.Handler{ID: 4001334, Kind: skills.KindAttack, Attack: melee(pickpocketOn)}) // Double Stab
	skills.Register(skills.Handler{ID: 4001344, Kind: skills.KindAttack, Attack: shoot()})             // Lucky Seven: LUK-based ceiling
}

// nimbleBody (4000000) adds x accuracy and y avoidability.
func nimbleBody(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{ACC: c.Values["x"], AVOID: c.Values["y"]}
}

// keenEyes (4000001) adds range to the claw range (PlayerStats defRange).
func keenEyes(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{Range: c.Values["range"]} }

// darkSight (4001003) hides the caster: DARK_SIGHT x and the WZ speed
// penalty (zero at max level). Legacy comments the local DARK_SIGHT out and only broadcasts the
// foreign one; the native client needs the local bit to enter Dark Sight.
func darkSight(c *skills.CastContext) (skills.CastPlan, error) {
	stats := []skills.StatValue{stat("DARK_SIGHT", c.Value("x"))}
	if v := c.Value("speed"); v != 0 {
		stats = append(stats, stat("SPEED", v))
	}
	return selfBuff(c, stats...)
}

// darkSightBreak is Java MapleCharacter.afterAttack: an attack that hits ends
// Dark Sight, unless a Dual Blade's Advanced Dark Sight (4330001) keeps it
// with its prop.
func darkSightBreak(c *skills.TriggerContext) *skills.BuffChange {
	if c.Attack == nil || len(c.Attack.Targets) == 0 || len(c.Rows) == 0 {
		return nil
	}
	if v := values(c.Character, c.Now, 4330001); v != nil && roll(v["prop"]) {
		return nil
	}
	return &skills.BuffChange{Cancel: true}
}
