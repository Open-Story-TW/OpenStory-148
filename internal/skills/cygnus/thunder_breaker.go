package cygnus

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"time"
)

// Thunder Breaker 1500, 1510, 1511.
func init() {
	passive(15000000, extremeDodge)                                                                // 極限迴避
	pvpOnly(15000005)                                                                              // 籤 攻擊: PvP only
	skills.Register(skills.Handler{ID: 15001001, Kind: skills.KindAttack, Attack: thunderMelee()}) // 衝擊拳
	skills.Register(skills.Handler{ID: 15001002, Kind: skills.KindAttack, Attack: thunderMelee()}) // 旋風斬
	skills.Register(skills.Handler{ID: 15001003, Kind: skills.KindBuff, Cast: dash})               // 衝鋒
	skills.Register(spirit(15001004, combat.Lightning))                                            // 閃電

	passive(15100001, mastery("acc", knuckle))                                                                                     // 精通指虎
	skills.Register(skills.Handler{ID: 15100004, Kind: skills.KindPassive | skills.KindBuff, OnAttack: energyCharge})              // 蓄能激發
	passive(15100007, maxHP)                                                                                                       // HP增加: Java percent_hp for 1510–1512
	passive(15100009, physicalTraining)                                                                                            // 百烈訓練
	skills.Register(skills.Handler{ID: 15101002, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})                              // 致命快打
	skills.Register(skills.Handler{ID: 15101003, Kind: skills.KindAttack, Attack: thunderMelee()})                                 // 狂暴衝擊: charge quirk is built into S0b
	skills.Register(skills.Handler{ID: 15101005, Kind: skills.KindAttack | skills.KindMobStatus, Attack: thunderMelee(hit(stun))}) // 能量暴擊
	// 雷鳴: WK_CHARGE x plus the generic mad; Java isPartyBuff does not
	// exclude it (unlike 11111007/12101005), so it charges the party.
	skills.Register(skills.Handler{ID: 15101006, Kind: skills.KindPartyBuff, Cast: party(func(c *skills.CastContext) (skills.CastPlan, error) {
		return buff(c, stat("WK_CHARGE", c.Value("x")))
	})})

	passive(15110000, critical("prop"))                                                                                    // 衝壓暴擊
	skills.Register(skills.Handler{ID: 15111001, Kind: skills.KindAttack | skills.KindHeal, Attack: thunderMelee(drainX)}) // 損人利己
	skills.Register(skills.Handler{ID: 15111002, Kind: skills.KindBuff | skills.KindMorph, Cast: morph(1000)})             // 鬥神附體
	skills.Register(skills.Handler{ID: 15111003, Kind: skills.KindAttack, Attack: thunderMelee()})                         // 衝擊波
	skills.Register(skills.Handler{ID: 15111004, Kind: skills.KindAttack, Attack: thunderMelee()})                         // 閃．連殺
	skills.Register(skills.Handler{ID: 15111005, Kind: skills.KindPartyBuff, Cast: party(value("SPEED_INFUSION", "x"))})   // 最終極速: hpCon and mpCon
	// 閃光擊: SPARK x on the caster only (Java sends no foreign buff); the
	// client then sends the spark hits as lightning attacks of the skill.
	skills.Register(skills.Handler{ID: 15111006, Kind: skills.KindBuff | skills.KindAttack, Cast: value("SPARK", "x"), Attack: melee(element(combat.Lightning))})
	skills.Register(skills.Handler{ID: 15111007, Kind: skills.KindAttack, Attack: attack(combat.Shoot, noAmmo, prepare(thunderCharged))}) // 鯨噬: Java ranged AOE
	skills.Register(skills.Handler{ID: 15111011, Kind: skills.KindBuff, Cast: dice})                                                      // 幸運骰子
}

// thunderMelee is a Thunder Breaker close-range attack; Lightning Charge
// makes it lightning.
func thunderMelee(opts ...option) *combat.AttackSpec {
	return melee(append(opts, prepare(thunderCharged))...)
}

// thunderCharged is Java DamageParse: an active WK_CHARGE from 15101006
// makes the attack lightning.
func thunderCharged(ctx *combat.AttackContext) error {
	if activeBuff(ctx.Character, ctx.Now, 15101006) != 0 {
		ctx.Effect.Element = combat.Lightning
	}
	return nil
}

// dash (15001003) is DASH_SPEED x and DASH_JUMP y (Java givePirate).
func dash(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Value("x") < 1 || c.Value("y") < 1 {
		return skills.StandardCost(c), fmt.Errorf("skill%d: invalid values", c.Skill.ID)
	}
	return buff(c, stat("DASH_SPEED", c.Value("x")), stat("DASH_JUMP", c.Value("y")))
}

// energyFull is the Energy Charge gauge maximum (Java handleEnergyCharge).
const energyFull = 10000

// energyCharge follows Java MapleCharacter.handleEnergyCharge for 1510–1512:
// the first attack starts an untimed gauge at 0, each later attack adds x
// per target line until 10000, and the attack after the gauge is full starts
// the timed full charge (WZ time). While the timed charge runs the gauge
// does not change; when it ends the next attack starts over.
func energyCharge(c *skills.TriggerContext) *skills.BuffChange {
	if j := c.Character.Job; j < 1510 || j > 1512 || c.Attack == nil || c.Attack.Summon || len(c.Attack.Targets) == 0 {
		return nil
	}
	gauge := func(v int, d time.Duration) *skills.BuffChange {
		return &skills.BuffChange{Buff: &skills.Buff{Duration: d, Stats: []skills.StatValue{stat("ENERGY_CHARGE", v)}}}
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
		return gauge(min(energyFull, v+c.Value("x")*len(c.Attack.Targets)*max(1, c.Attack.Lines)), skills.Permanent)
	case c.Value("time") > 0:
		return gauge(energyFull, time.Duration(c.Value("time"))*time.Second)
	}
	return nil
}

// dice (15111011) rolls a six-faced die into DICE_ROLL; the client derives
// the effect from the roll and the WZ values. A 1 has no effect but MP and
// cooldown are spent. Legacy refuses the cast and hard-codes the roll to 5
// and the time to 90 s (debug overrides); this uses a real roll and the WZ
// time, like the pirate dice.
func dice(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("time") < 1 {
		return p, fmt.Errorf("skill%d: no time", c.Skill.ID)
	}
	if r := rollDie(6); r > 1 {
		p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: []skills.StatValue{stat("DICE_ROLL", r)}}
	}
	return p, nil
}

func extremeDodge(c *skills.PassiveContext) skills.Bonus {
	if _, original := c.Values["accX"]; !original {
		return accAvoid(c)
	}
	return skills.Bonus{ACC: c.Values["accX"], Jump: c.Values["psdJump"], SpeedLimitBonus: c.Values["speedMax"]}
}
