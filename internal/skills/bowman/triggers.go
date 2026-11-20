package bowman

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
)

func init() {
	skills.Register(skills.Handler{ID: 3110001, Kind: skills.KindPassive}) // Mortal Blow: mortalCeiling / mortalHeal
	skills.Register(skills.Handler{ID: 3210001, Kind: skills.KindPassive}) // Mortal Blow
	combat.ModifyCeiling(mortalCeiling)
	combat.ModifyDrain(mortalHeal)
}

// mortal returns the WZ values of the attacker's Mortal Blow (3110001 for
// 311/312, 3210001 for 321/322), nil when not learned (Java DamageParse).
func mortal(c domain.Character) map[string]int {
	id := 0
	switch c.Job {
	case 311, 312:
		id = 3110001
	case 321, 322:
		id = 3210001
	}
	lv := learned(c, id)
	if lv < 1 {
		return nil
	}
	def, err := content.ConfiguredSkillDefinition(id)
	if err != nil {
		return nil
	}
	v, err := def.Values(min(lv, def.MaxLevel))
	if err != nil {
		return nil
	}
	return v
}

// below reports a target HP under x percent of its maximum.
func below(hp, maxHP int64, x int) bool {
	return maxHP > 0 && hp > 0 && hp*100 < maxHP*int64(x)
}

// mortalCeiling: below x% HP the damage is undefined in Java, so a line may
// take the target's remaining HP.
func mortalCeiling(dc combat.DamageContext, ceiling int) int {
	v := mortal(dc.Attacker)
	if v == nil || !below(dc.HP, dc.Mob.HP, v["x"]) {
		return ceiling
	}
	return max(ceiling, int(min(dc.HP, int64(combat.MaxLineDamage()))))
}

// mortalHeal recovers z% of MaxHP for every target under x% HP (Java adds it
// inside the per-monster loop).
func mortalHeal(ctx *combat.AttackContext, _ int64) (int, int) {
	v := mortal(ctx.Character)
	if v == nil || v["z"] < 1 {
		return 0, 0
	}
	hp := 0
	for _, t := range ctx.Targets {
		if below(t.HP, t.Mob.HP, v["x"]) {
			hp += ctx.Character.MaxHP * v["z"] / 100
		}
	}
	return hp, 0
}
