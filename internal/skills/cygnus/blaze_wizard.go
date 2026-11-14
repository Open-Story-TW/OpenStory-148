package cygnus

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// Blaze Wizard 1200, 1210, 1211.
func init() {
	passive(12000005, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{MaxMPPercent: c.Values["mmpR"]} }) // MP增加
	pvpOnly(12000006)                                                                                                      // 自然力變弱: ELEMENT_WEAKEN is PvP only
	skills.Register(skills.Handler{ID: 12001001, Kind: skills.KindBuff, Cast: value("MAGIC_GUARD", "x")})                  // 魔心防禦
	skills.Register(skills.Handler{ID: 12001002, Kind: skills.KindBuff, Cast: generic})                                    // 魔力之盾: pdd, mdd
	blaze(12001003)                                                                                                        // 魔力爪
	skills.Register(spirit(12001004, combat.Fire))                                                                         // 火牢術 (Flame)

	passive(12100007, mastery("matk", wand, staff))                                                                                                                                 // 咒語精通
	passive(12100008, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{INT: c.Values["intX"]} })                                                                   // 智慧昇華
	skills.Register(skills.Handler{ID: 12101000, Kind: skills.KindPartyBuff, Cast: party(generic)})                                                                                 // 精神強力: mad
	skills.Register(skills.Handler{ID: 12101001, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: mobCast(combat.MobStatusApply{Stat: combat.MobSpeed, ValueFrom: "x"})}) // 緩速術
	blaze(12101002)                                                                                                                                                                 // 火焰箭
	skills.Register(skills.Handler{ID: 12101003, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                                                                  // 瞬間移動
	skills.Register(skills.Handler{ID: 12101004, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})                                                                               // 極速詠唱
	skills.Register(skills.Handler{ID: 12101005, Kind: skills.KindBuff, Cast: value("ELEMENT_RESET", "x")})                                                                         // 自然力重置: self only
	blaze(12101006)                                                                                                                                                                 // 火柱

	passive(12110000, critical("cr")) // 魔法暴擊
	passive(12110001, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{MPConsume: c.Values["x"] - 100, ElementAmp: c.Values["y"] - 100}
	}) // 魔力激發
	skills.Register(skills.Handler{ID: 12111002, Kind: skills.KindMobStatus, Cast: skills.CostOnly, Attack: mobCast(combat.MobStatusApply{Stat: combat.MobSeal, Value: 1})}) // 魔法封印
	blaze(12111003)                                                                                                                                                          // 火流星: dot
	ifrit := spirit(12111004, combat.Fire)                                                                                                                                   // 召喚冰魔 (Ifrit)
	ifrit.Passive = func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{TER: c.Values["terR"]} }
	skills.Register(ifrit)
	// 火牢術屏障: a magic attack that leaves a poison mist (Java MapleMist
	// isPoisonMist 1), like Poison Mist 2111003.
	skills.Register(skills.Handler{ID: 12111005, Kind: skills.KindAttack | skills.KindFieldObject | skills.KindMobStatus, Attack: blazeSpec(12111005, poisonMist(12111005))})
	blaze(12111006)                                                                                                                              // 火風暴
	skills.Register(skills.Handler{ID: 12111007, Kind: skills.KindBuff | skills.KindAttack, Cast: teleportMastery, Attack: withBody(blazeSpec(12111007))}) // 瞬間移動精通
}

// blazeElements is the WZ elemAttr of the Blaze Wizard attacks (Skill.wz
// 12xx.img).
var blazeElements = map[int]combat.Element{
	12001003: combat.Fire, 12101002: combat.Fire, 12101006: combat.Fire, 12111003: combat.Fire, 12111005: combat.Fire, 12111006: combat.Fire,
}

// blazeSpec is a Blaze Wizard magic attack: the WZ element (neutral while
// Elemental Reset is on, Java DamageParse) and POISON when the level has a
// dot (Java isPoison).
func blazeSpec(id int, opts ...option) *combat.AttackSpec {
	e := blazeElements[id]
	opts = append([]option{prepare(func(ctx *combat.AttackContext) error {
		ctx.Effect.Element = e
		if activeBuff(ctx.Character, ctx.Now, 12101005) != 0 {
			ctx.Effect.Element = combat.Physical
		}
		if ctx.Effect.DOT > 0 && ctx.Effect.DOTTime > 0 {
			ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, poison)
		}
		return nil
	})}, opts...)
	return magic(opts...)
}

func blaze(id int) {
	skills.Register(skills.Handler{ID: id, Kind: skills.KindAttack, Attack: blazeSpec(id)})
}

// teleportMastery (12111007) toggles TELEPORT_MASTERY x until recast (Java
// 2100000000 ms) and charges y MP instead of mpCon; teleporting with it on
// is a magic attack.
func teleportMastery(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Value("x") < 1 || c.Value("y") < 0 {
		return skills.CastPlan{}, fmt.Errorf("skill%d: invalid values", c.Skill.ID)
	}
	return skills.CastPlan{MPCost: c.Value("y"), Toggle: true, Self: &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{stat("TELEPORT_MASTERY", c.Value("x"))}}}, nil
}

// withBody also accepts the spec's teleport body attack (0x4E, Teleport
// Mastery); PlanAttack keeps the magic ceiling for it.
func withBody(s *combat.AttackSpec) *combat.AttackSpec {
	s.Kind |= combat.Body
	return s
}
