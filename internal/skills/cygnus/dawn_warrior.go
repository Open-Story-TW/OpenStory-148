package cygnus

import (
	"errors"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"time"
)

// Dawn Warrior 1100, 1110, 1111.
func init() {
	passive(11000005, maxHP)                                                            // HP增加: Java percent_hp for 1100–1112
	pvpOnly(11000006)                                                                   // 守護拳套: PvP only
	skills.Register(skills.Handler{ID: 11001001, Kind: skills.KindBuff, Cast: generic}) // 自身強化: pdd
	skills.Register(skills.Handler{ID: 11001002, Kind: skills.KindAttack, Attack: dawnMelee()})
	skills.Register(skills.Handler{ID: 11001003, Kind: skills.KindAttack, Attack: dawnMelee()})
	skills.Register(spirit(11001004, combat.Holy)) // 靈魂

	passive(11100000, mastery("acc", oneHandedSword, oneHandedAxe, oneHandedBlunt, twoHandedSword, twoHandedAxe, twoHandedBlunt)) // 精準之劍
	passive(11100007, physicalTraining)
	skills.Register(skills.Handler{ID: 11101001, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})                                // 快速之劍
	skills.Register(skills.Handler{ID: 11101002, Kind: skills.KindBuff | skills.KindAttack, Cast: finalAttack, Attack: dawnMelee()}) // 終極攻擊
	skills.Register(skills.Handler{ID: 11101003, Kind: skills.KindPartyBuff, Cast: party(generic)})                                  // 憤怒: pad
	skills.Register(skills.Handler{ID: 11101004, Kind: skills.KindAttack, Attack: shoot(noAmmo, prepare(soulCharged))})              // 靈魂之刃: ranged AOE without ammo
	skills.Register(skills.Handler{ID: 11101005, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                   // 靈魂迅移 (movement)
	skills.Register(skills.Handler{ID: 11101006, Kind: skills.KindBuff, Cast: value("POWERGUARD", "x")})                             // 反射之盾

	skills.Register(skills.Handler{ID: 11110000, Kind: skills.KindPassive, Passive: recovery, OnTick: skills.ReviewedRecoveryTick, TickEvery: 4 * time.Second}) // 魔力恢復
	skills.Register(skills.Handler{ID: 11110005, Kind: skills.KindPassive})                                                                                     // 進階鬥氣: read by the combo counter
	skills.Register(skills.Handler{ID: 11111001, Kind: skills.KindBuff, Cast: combo, OnAttack: comboOrbs})                                                      // 鬥氣集中
	skills.Register(skills.Handler{ID: 11111002, Kind: skills.KindAttack | skills.KindMobStatus, Attack: dawnMelee(hit(darkness), prepare(finisher))})          // 黑暗之劍: 1 orb
	skills.Register(skills.Handler{ID: 11111003, Kind: skills.KindAttack | skills.KindMobStatus, Attack: dawnMelee(hit(stun), prepare(finisher))})              // 昏迷之劍: 2 orbs
	skills.Register(skills.Handler{ID: 11111004, Kind: skills.KindAttack, Attack: dawnMelee()})                                                                 // 雙連斬: ignoreMobpdpR
	skills.Register(skills.Handler{ID: 11111006, Kind: skills.KindAttack, Attack: dawnMelee()})                                                                 // 靈魂突刺
	skills.Register(skills.Handler{ID: 11111007, Kind: skills.KindBuff, Cast: soulCharge})                                                                      // 靈魂屬性
	// 魔防消除: Legacy gives it no monster status (not in the MAGIC_CRASH
	// list) and lists it with the hidden skills; the cast only pays.
	skills.Register(skills.Handler{ID: 11111008, Kind: skills.KindSpecial, Cast: skills.CostOnly})
}

// dawnMelee is a Dawn Warrior close-range attack; Soul Charge makes it holy.
func dawnMelee(opts ...option) *combat.AttackSpec {
	return melee(append(opts, prepare(soulCharged))...)
}

// soulCharged is Java DamageParse: an active WK_CHARGE from 11111007 makes
// the attack holy.
func soulCharged(ctx *combat.AttackContext) error {
	if activeBuff(ctx.Character, ctx.Now, 11111007) != 0 {
		ctx.Effect.Element = combat.Holy
	}
	return nil
}

// soulCharge (11111007) is WK_CHARGE x plus the generic mad statup, self
// only (Java isPartyBuff excludes it).
func soulCharge(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("WK_CHARGE", c.Value("x")))
}

// finalAttack (11101002, 13101002) is FINAL_SHOOT_ATTACK x for the WZ time;
// the client then sends the final-attack hits as attacks of the skill.
func finalAttack(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("FINAL_SHOOT_ATTACK", c.Value("x")))
}

// recovery (11110000) is Java relocHeal: hp and mp more per regeneration
// tick.
func recovery(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{HPRecovery: c.Values["hp"], MPRecovery: c.Values["mp"]}
}

// combo (11111001) starts the orb counter at COMBO 1 (no orbs).
func combo(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("COMBO", 1))
}

// darkness is Java MonsterStatus.DARKNESS = x (11111002).
var darkness = combat.MobStatusApply{Stat: combat.MobDarkness, ValueFrom: "x"}

// finisher is Java PlayerHandler.isFinisher for 11111002/11111003: rejected
// without orbs; the damage ceiling scales by the orb count. The combo
// trigger consumes the orbs afterwards.
func finisher(ctx *combat.AttackContext) error {
	v, ok := skills.ActiveStat(ctx.Character, "COMBO", ctx.Now)
	if !ok || v < 2 {
		return errors.New("finisher needs combo orbs")
	}
	ctx.Effect.Damage *= v - 1
	return nil
}

// comboOrbs is Java afterAttack/handleOrbgain/handleOrbconsume for jobs
// 1111–1112 (another job keeps a leftover COMBO unchanged):
// 11111002 uses 1 orb and 11111003 2 (never below COMBO 1); any other attack
// adds one orb up to x (11110005's x when learned, which also adds a second
// orb at its prop). The remaining duration is kept.
func comboOrbs(c *skills.TriggerContext) *skills.BuffChange {
	v, ok := c.Stat("COMBO")
	if j := c.Character.Job; !ok || len(c.Rows) == 0 || c.Attack == nil || j != 1111 && j != 1112 {
		return nil
	}
	next := v
	switch c.Attack.SkillID {
	case 11111001:
		return nil
	case 11111002:
		next = max(1, v-1)
	case 11111003:
		next = max(1, v-2)
	default:
		limit, prop := c.Value("x"), 0
		if adv := values(c.Character, c.Now, 11110005); adv != nil {
			limit, prop = adv["x"], adv["prop"]
		}
		if v >= limit+1 {
			return nil
		}
		next = v + 1
		if prop > 0 && next < limit+1 && roll(prop) {
			next++
		}
	}
	if next == v {
		return nil
	}
	return &skills.BuffChange{Buff: &skills.Buff{Duration: c.Rows[0].ExpiresAt.Sub(c.Now), Stats: []skills.StatValue{stat("COMBO", next)}}}
}
