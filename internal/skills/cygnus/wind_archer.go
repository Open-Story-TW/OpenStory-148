package cygnus

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"time"
)

// Wind Archer 1300, 1310, 1311.
func init() {
	passive(13000000, critical("prop"))                             // 霸王箭: Java critSkill for 1300–1312
	passive(13000001, func(c *skills.PassiveContext) skills.Bonus { // 精通射手
		return skills.Bonus{
			ACC: c.Values["acc"], AVOID: c.Values["eva"], Speed: c.Values["speed"],
			Range: c.Values["range"], SpeedLimitBonus: c.Values["speedMax"],
		}
	})
	pvpOnly(13000005)                                                                           // 自然吸血術: PvP only
	skills.Register(skills.Handler{ID: 13001003, Kind: skills.KindAttack, Attack: windShoot()}) // 二連箭
	skills.Register(spirit(13001004, combat.Physical))                                          // 暴風 (Storm)

	passive(13100000, func(c *skills.PassiveContext) skills.Bonus { // 精準之弓
		if weaponType(c.Character) != bow {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.BasicMastery(13110003), ACC: c.Values["x"]}
	})
	passive(13100008, physicalTraining)
	skills.Register(skills.Handler{ID: 13101001, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})                                      // 快速之箭
	skills.Register(skills.Handler{ID: 13101002, Kind: skills.KindBuff | skills.KindAttack, Cast: finalAttack, Attack: windShoot()})       // 終極攻擊
	skills.Register(skills.Handler{ID: 13101003, Kind: skills.KindBuff, Cast: soulArrow})                                                  // 無形之箭
	skills.Register(skills.Handler{ID: 13101004, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                         // 雙重跳躍
	skills.Register(skills.Handler{ID: 13101005, Kind: skills.KindAttack, Attack: windShoot(noAmmo)})                                      // 暴風射擊: Java AOE
	skills.Register(skills.Handler{ID: 13101006, Kind: skills.KindBuff, Cast: windWalk, Passive: windWalkDamage, OnAttack: breakOnAttack}) // 風影漫步
	skills.Register(skills.Handler{ID: 13101007, Kind: skills.KindAttack, Attack: windShoot()})                                            // 四連箭

	passive(13110003, func(c *skills.PassiveContext) skills.Bonus { // 弓術精通
		b := skills.Bonus{WATK: c.Values["x"], CriticalDamageMin: c.Values["criticaldamageMin"]}
		if weaponType(c.Character) == bow {
			b.Mastery = c.Values["mastery"]
		}
		return b
	})
	skills.Register(skills.Handler{ID: 13110008, Kind: skills.KindPassive,
		Passive:   func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{AvoidRate: c.Values["er"]} },
		OnDamaged: windEvasionDodge,
	}) // 躲避: dodgeChance + 1s guaranteed critical on dodge
	skills.Register(skills.Handler{ID: 13110009, Kind: skills.KindPassive})                                                              // 致命箭 Mortal Blow (v148 0x60b6cd alongside 3110001/3210001)
	skills.Register(skills.Handler{ID: 13111000, Kind: skills.KindAttack | skills.KindMobStatus, Attack: windShoot(noAmmo, hit(stun))}) // 箭雨: v148 WZ prop and time
	skills.Register(skills.Handler{ID: 13111001, Kind: skills.KindBuff, Cast: value("CONCENTRATE", "x")})                               // 集中: CONCENTRATE x, epad
	skills.Register(skills.Handler{ID: 13111002, Kind: skills.KindAttack, Attack: windShoot()})                                         // 暴風神射: keydown quirk is built into S0b
	skills.Register(skills.Handler{ID: 13111004, Kind: skills.KindSummon, Cast: summonBuff("PUPPET"), Attack: puppet})                  // 替身術
	skills.Register(skills.Handler{ID: 13111005, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1003)})                          // 阿爾法 (Eagle Eye)
	skills.Register(skills.Handler{ID: 13111006, Kind: skills.KindAttack, Attack: windShoot()})                                         // 疾風光速神弩: Legacy has no drain for it
	skills.Register(skills.Handler{ID: 13111007, Kind: skills.KindAttack, Attack: windShoot(noAmmo)})                                   // 疾風掃射: Java AOE
}

// windEvasionDodge (13110008) grants 100% critical rate for WZ time seconds
// after dodging a monster hit.
func windEvasionDodge(c *skills.TriggerContext) *skills.BuffChange {
	if c.Damage != 0 || c.Mob == 0 {
		return nil
	}
	secs := max(1, c.Value("time"))
	return &skills.BuffChange{
		Buff: &skills.Buff{
			Duration: time.Duration(secs) * time.Second,
			Stats:    []skills.StatValue{{Stat: "CRITICAL_RATE", Value: 100}},
		},
	}
}

// windShoot is a Wind Archer shoot attack; Soul Arrow shoots without arrows
// (Java rangedAttack).
func windShoot(opts ...option) *combat.AttackSpec {
	return shoot(append(opts, prepare(func(ctx *combat.AttackContext) error {
		if _, ok := skills.ActiveStat(ctx.Character, "SOULARROW", ctx.Now); ok {
			ctx.NoAmmo = true
		}
		return nil
	}))...)
}

// soulArrow (13101003) is SOULARROW x and needs a bow (WZ weapon field).
func soulArrow(c *skills.CastContext) (skills.CastPlan, error) {
	if weaponType(c.Caster) != bow {
		return skills.StandardCost(c), fmt.Errorf("skill%d: needs a bow", c.Skill.ID)
	}
	return buff(c, stat("SOULARROW", c.Value("x")))
}

// windWalk (13101006) is WIND_WALK x plus the generic speed penalty (zero at
// the max level). Legacy refuses the cast ("此技能暫時無法使用"), a server
// hack; this follows the effect.
func windWalk(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("WIND_WALK", c.Value("x")))
}

// windWalkDamage is Java PlayerStats: while WIND_WALK is up, damage is
// multiplied by damage%; afterAttack then ends it.
func windWalkDamage(c *skills.PassiveContext) skills.Bonus {
	if !c.Active(13101006) {
		return skills.Bonus{}
	}
	return skills.Bonus{DamagePercent: c.Values["damage"] - 100}
}

// breakOnAttack is Java MapleCharacter.afterAttack: any attack that hits
// ends Wind Walk and Dark Sight.
func breakOnAttack(c *skills.TriggerContext) *skills.BuffChange {
	if c.Attack == nil || c.Attack.Summon || len(c.Attack.Targets) == 0 || len(c.Rows) == 0 {
		return nil
	}
	return &skills.BuffChange{Cancel: true}
}

// puppet (13111004) is a stationary decoy with WZ x HP (Java isPuppet,
// getSummonType 0).
var puppet = &combat.AttackSpec{Kind: noAttackPacket, Summon: &combat.SummonSpec{MoveAbility: stationary, Puppet: true}}
