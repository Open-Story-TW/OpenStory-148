package cygnus

import (
	"fmt"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Night Walker 1400, 1410, 1411.
func init() {
	passive(14000000, accAvoid)                                                                                      // 幻體功
	passive(14000001, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{Range: c.Values["range"]} }) // 鷹之眼: Java defRange
	pvpOnly(14000006)                                                                                                // 魔心妙手術: PvP only
	// 詛咒術: Java monsterStatus WATK x, WDEF y on hit, like Disorder 4001002.
	skills.Register(skills.Handler{ID: 14001002, Kind: skills.KindAttack | skills.KindMobStatus, Attack: melee(hit(
		combat.MobStatusApply{Stat: combat.MobPAD, ValueFrom: "x"}, combat.MobStatusApply{Stat: combat.MobPDR, ValueFrom: "y"}))})
	skills.Register(skills.Handler{ID: 14001003, Kind: skills.KindBuff, Cast: darkSight, Passive: darkSightDamage, OnAttack: breakOnAttack}) // 隱身術
	skills.Register(skills.Handler{ID: 14001004, Kind: skills.KindAttack, Attack: nightShoot()})                                             // 雙飛斬 (Lucky Seven)
	skills.Register(spirit(14001005, combat.Physical))                                                                                       // 黑暗雷鳥

	passive(14100000, mastery("acc", claw))                         // 精準暗器: GameConstants.getMasterySkill
	passive(14100001, func(c *skills.PassiveContext) skills.Bonus { // 強力投擲
		if weaponType(c.Character) != claw {
			return skills.Bonus{}
		}
		// The original claw calculation reads the skill's damage field for the minimum critical bonus.
		return skills.Bonus{Critical: c.Values["prop"], CriticalDamageMin: c.Values["damage"]}
	})
	pvpOnly(14100005) // 驅逐: Legacy never reads it
	passive(14100010, physicalTraining)
	skills.Register(skills.Handler{ID: 14101002, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")}) // 極速暗殺
	skills.Register(skills.Handler{ID: 14101003, Kind: skills.KindPartyBuff, Cast: party(generic)})   // 速度激發: speed, jump
	skills.Register(skills.Handler{ID: 14101004, Kind: skills.KindSpecial, Cast: skills.CostOnly})    // 二段跳
	// 吸血: WZ type 1, but Legacy also lists it with the ranged AOE skills;
	// accept both packets until MCP shows which one the client sends.
	skills.Register(skills.Handler{ID: 14101006, Kind: skills.KindAttack | skills.KindHeal, Attack: attack(combat.Melee|combat.Shoot, noAmmo, drainX)})

	passive(14110003, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{PotionPercent: c.Values["x"] - 100} })   // 藥劑精通: Java RecoveryUP
	skills.Register(skills.Handler{ID: 14110004, Kind: skills.KindPassive | skills.KindMobStatus})                               // 飛毒殺: POISON on venom attacks
	skills.Register(skills.Handler{ID: 14111000, Kind: skills.KindBuff, Cast: value("SHADOWPARTNER", "x")})                      // 影分身: itemCon 4006001
	skills.Register(skills.Handler{ID: 14111001, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: mobCast(shadowWeb)}) // 影網術
	skills.Register(skills.Handler{ID: 14111002, Kind: skills.KindAttack, Attack: nightShoot()})                                 // 風魔手裏劍: bulletConsume 3
	skills.Register(skills.Handler{ID: 14111005, Kind: skills.KindAttack, Attack: nightShoot()})                                 // 三飛閃: ignoreMobpdpR
	// 毒炸彈: Legacy parses it as a charged close-range attack and leaves a
	// poison mist (MapleMist isPoisonMist 1).
	skills.Register(skills.Handler{ID: 14111006, Kind: skills.KindAttack | skills.KindFieldObject | skills.KindMobStatus, Attack: melee(hit(poison), poisonMist(14111006))})
	skills.Register(skills.Handler{ID: 14111007, Kind: skills.KindBuff, Cast: nightShadowStars})
	// shadeSplit calls the v148 CUserLocal shoot sender through 0x93920F.
	skills.Register(skills.Handler{ID: 14111008, Kind: skills.KindAttack, Attack: nightShoot()})
	// 黑暗殺: v148 CSummoned::Init (0x73AACF) handles 14111010 with Dark Flare
	// (4111007/4211007) as a stationary reflect summon.
	skills.Register(skills.Handler{ID: 14111010, Kind: skills.KindSummon, Cast: summonBuff("SUMMON"),
		Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 7}}})
}

// nightShoot applies Night Walker's Shadow Stars and Venom to its star attacks.
func nightShoot() *combat.AttackSpec {
	return shoot(prepare(func(ctx *combat.AttackContext) error {
		if skills.ActiveBuff(ctx.Character, 14111007, ctx.Now) {
			ctx.NoAmmo = true
		}
		return venom(ctx)
	}))
}

// nightShadowStars consumes the v148 WZ bulletConsume once and grants
// SPIRIT_CLAW for the WZ duration. v148 CUserLocal::FindAmmo (0x8EF0AA..0x8EF0F6)
// ignores SPIRIT_CLAW <= 0 and resolves the star ID as SPIRIT_CLAW + 2069999.
func nightShadowStars(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	base, err := combat.BaseStats(c.Caster, c.Now)
	if err != nil || combat.WeaponFor(base.WeaponID).Projectile != combat.AmmoStar {
		return p, fmt.Errorf("skill%d: equip a claw", c.Skill.ID)
	}
	quantity := c.Value("bulletConsume")
	if quantity <= 0 {
		return p, fmt.Errorf("skill%d: missing throwing star cost", c.Skill.ID)
	}
	itemID, slot := 0, 0
	for _, item := range c.Caster.Items {
		if item.InvType == 2 && item.Slot > 0 && item.Quantity >= quantity &&
			combat.AmmoOf(item.TemplateID) == combat.AmmoStar && (slot == 0 || item.Slot < slot) {
			itemID, slot = item.TemplateID, item.Slot
		}
	}
	if itemID == 0 {
		return p, fmt.Errorf("skill%d: requires %d throwing stars", c.Skill.ID, quantity)
	}
	p, err = buff(c, stat("SPIRIT_CLAW", max(1, itemID-2069999)))
	if err != nil {
		return p, err
	}
	p.Items = append(p.Items, skills.ItemCost{ItemID: itemID, Quantity: quantity})
	return p, nil
}

// venom adds 14110004's POISON when learned, rolled with its prop: a tick of
// dot% of the max base damage for dotTime.
func venom(ctx *combat.AttackContext) error {
	v := values(ctx.Character, ctx.Now, 14110004)
	if v == nil {
		return nil
	}
	tick := max(1, int64(v["dot"])*combat.MaxBaseDamage(ctx.Stats)/100)
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{
		Stat: combat.MobPoison, Poison: true, Value: int(min(tick, 0x7fffffff)),
		Prop: max(1, v["prop"]), Duration: time.Duration(v["dotTime"]) * time.Second,
	})
	return nil
}

// shadowWeb is Java SHADOW_WEB (14111001): binds and ticks the monster's max
// HP / 50.
var shadowWeb = combat.MobStatusApply{Stat: combat.MobWeb, Poison: true, TickFrom: "mobhp50"}

// darkSight (14001003) is DARK_SIGHT x and the generic speed penalty. Legacy
// comments the local statup out and only broadcasts the foreign one; the
// native client needs the local bit (as for 4001003).
func darkSight(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("DARK_SIGHT", c.Value("x")))
}

// darkSightDamage is Java PlayerStats: the attack out of Dark Sight deals y%
// more (getBuffedSkill_Y(DARK_SIGHT)); afterAttack then ends it.
func darkSightDamage(c *skills.PassiveContext) skills.Bonus {
	if !c.Active(14001003) {
		return skills.Bonus{}
	}
	return skills.Bonus{DamagePercent: c.Values["y"]}
}
