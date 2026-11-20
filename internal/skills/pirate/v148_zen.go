package pirate

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Regional 57xx attacks the v148 client sends but no handler covered. The
// family follows the native sender that references each id: the 0x55
// melee sender 0x91A08F for Panda Kill and Dragon Fist, the shoot sender
// 0x90B36D (and the gun dispatcher 0x92D2E4 for Tiger Charge) for the
// others. None of them lists a bulletCount, so no bullet is consumed.
func init() {
	for _, a := range []attack{
		{id: 5701001, kind: combat.Melee},               // 熊貓殺 Panda Kill
		{id: 5711006, kind: combat.Melee},               // 龍拳 Dragon Fist
		{id: 5711002, kind: combat.Shoot, noAmmo: true}, // 猛虎衝 Tiger Charge
		// The x% explosion after Tiger Charge (its w); both senders reference it.
		{id: 5710012, kind: combat.Melee | combat.Shoot, noAmmo: true, levelFrom: 5711002},
		{id: 5721004, kind: combat.Shoot, noAmmo: true},                            // 蒼龍燎原 Azure Dragon
		{id: 5721006, kind: combat.Shoot, noAmmo: true, maxDamage: dragonSoulLine}, // 龍魂一擊 Dragon Soul Strike
	} {
		skills.Register(skills.Handler{ID: a.id, Kind: skills.KindAttack, Attack: spec(a), LevelFrom: a.levelFrom})
	}
}

// dragonSoulLine (5721006) follows Snipe (3221007) in the v148 physical
// calc 0x71160B: against a non-boss each line is 989999 plus a roll up to
// 10000; against a boss the WZ cr is 100, so every line is critical.
func dragonSoulLine(c combat.DamageContext) int {
	if !c.Mob.Boss && !c.Mob.Invincible {
		return combat.MaxLineDamage()
	}
	return combat.CritCeiling(c, combat.LineCeiling(c))
}
