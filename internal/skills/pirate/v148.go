package pirate

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"os"
)

// The Buccaneer and Corsair skills the v148 client has and v145 did not.
// Values come from the built-in v148 books; nothing here has a Java
// reference, so each rule says what it takes from the WZ data. Skills marked
// "cost only" pay their cost and show the cast, and their effect is not
// applied yet.
var v148Attacks = []attack{
	// 海盜 500 (the two hidden ids are what the client sends for the basic attacks)
	{id: 5001008, kind: combat.Melee | combat.Shoot}, // hidden ranged basic: bulletCount 3
	{id: 5001009, kind: combat.Melee | combat.Shoot}, // hidden close basic: mobCount 6
	// 打手 510, 格鬥家 511, 拳霸 512
	{id: 5101012, kind: combat.Melee, prepare: barrelRoll},         // 颶風拳: the caster rushes x px, so the box reaches that far (see barrelRoll)
	{id: 5111009, kind: combat.Melee},                              // 雙重螺旋
	{id: 5111012, kind: combat.Melee},                              // 衝擊波
	{id: 5121013, kind: combat.Melee | combat.Shoot, noAmmo: true}, // 戰艦鯨魚號
	{id: 5121016, kind: combat.Shoot, noAmmo: true, prepare: requiresAttackBuff(5110001, "ENERGY_CHARGE", energyFull)}, // 能量爆發
	// 槍手 520, 神槍手 521, 槍神 522
	{id: 5200010, kind: combat.Shoot},                              // 三重火力: bulletCount 3
	{id: 5211008, kind: combat.Shoot},                              // 雙重槍閃: bulletConsume 2
	{id: 5211010, kind: combat.Shoot},                              // 彈丸潰擊
	{id: 5221013, kind: combat.Melee | combat.Shoot, noAmmo: true}, // 戰艦鯨魚號
	{id: 5221015, kind: combat.Shoot},                              // 繼續瞄準
	{id: 5221016, kind: combat.Shoot},                              // 爆頭射擊
	{id: 5221017, kind: combat.Shoot},                              // 霸王章魚砲台: bulletConsume 4
}

const summonCrew = 5211011

var crewVariants = []int{summonCrew, 5211015, 5211016}

func init() {
	for _, a := range v148Attacks {
		skills.Register(skills.Handler{ID: a.id, Kind: skills.KindAttack, Attack: spec(a)})
	}
	skills.Register(skills.Handler{ID: summonCrew, Kind: skills.KindSummon, Cast: valueOne("SUMMON"), Attack: crewSpec(crewVariants)})
	for _, id := range crewVariants[1:] {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSummon, LevelFrom: summonCrew, Attack: crewSpec(nil)})
	}
	octopus := &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 1, Multi: true}}
	skills.Register(skills.Handler{ID: 5211014, Kind: skills.KindSummon, Cast: valueOne("PUPPET"), Attack: octopus})
	passive := func(id int, f func(*skills.PassiveContext) skills.Bonus) {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
	}
	training := func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
	}
	passive(5000007, critical("cr"))                               // 初階爆擊: cr, criticaldamageMin
	passive(5100010, training)                                     // 體能訓練
	passive(5200009, training)                                     // 體能訓練
	passive(5110011, func(c *skills.PassiveContext) skills.Bonus { // 爆擊鬥氣
		return skills.Bonus{
			Critical: c.Values["cr"], BossCritical: c.Values["prop"],
			CriticalDamageMin: c.Values["criticaldamageMin"], CriticalDamage: c.Values["criticaldamageMax"],
		}
	})
	passive(5120014, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{FullDefenseIgnore: c.Values["x"] > 0}
	})
	passive(5210012, func(c *skills.PassiveContext) skills.Bonus { // 神槍手耐性
		return skills.Bonus{WDEFPercent: c.Values["pddR"], MDEFPercent: c.Values["mddR"], MaxHP: c.Values["mhpX"], MaxMP: c.Values["mmpX"]}
	})
	passive(5210013, func(c *skills.PassiveContext) skills.Bonus { // 金屬外殼
		return skills.Bonus{Critical: c.Values["cr"], IgnoreDefense: c.Values["ignoreMobpdpR"]}
	})
	// Gun Expert is skillType 3 in v148. The client sends its extra hit as
	// a separate 0x56 frame with the same tick as the triggering attack.
	skills.Register(skills.Handler{
		ID: 5220020, Kind: skills.KindPassive | skills.KindAttack,
		Attack: &combat.AttackSpec{
			Kind: combat.Shoot, Ammo: combat.AmmoNone,
			Prepare: func(c *combat.AttackContext) error {
				if weaponType(c.Character) != gun {
					return combat.ErrRejected
				}
				return nil
			},
		},
		Passive: func(c *skills.PassiveContext) skills.Bonus {
			if weaponType(c.Character) != gun {
				return skills.Bonus{}
			}
			return skills.Bonus{Mastery: c.Values["mastery"]}
		},
	})
	skills.Register(skills.Handler{ID: 5100013, Kind: skills.KindPassive}) // 耐久: see the server's periodic recovery
	skills.Register(skills.Handler{ID: 5700005, Kind: skills.KindPassive}) // 血脈通暢: periodic max-HP and max-MP recovery
	passive(5220019, func(c *skills.PassiveContext) skills.Bonus {         // 船員指令
		if c.Active(summonCrew) {
			return skills.Bonus{WATK: c.Values["pad"]}
		}
		return skills.Bonus{}
	})

	skills.Register(skills.Handler{ID: 5101011, Kind: skills.KindBuff, Cast: attackBuff("indiePad", "acc")}) // 全神貫注
	skills.Register(skills.Handler{ID: 5201008, Kind: skills.KindBuff, Cast: infiniteBullets})               // 無形彈藥
	skills.Register(skills.Handler{ID: 5201011, Kind: skills.KindSpecial, Cast: skills.CostOnly})            // 飛翼
	// 拳霸大師 Crossbones: active WATK% buff via BIT_197 (read in v148 GetPAD 0x7057B1)
	// plus passive knuckle mastery and abnormal/elemental status resistances.
	skills.Register(skills.Handler{ID: 5121015, Kind: skills.KindBuff | skills.KindPassive, Cast: valueBuff("BIT_197", "x"),
		Passive: func(c *skills.PassiveContext) skills.Bonus {
			b := skills.Bonus{ASR: c.Values["asrR"], TER: c.Values["terR"]}
			if weaponType(c.Character) == knuckle {
				b.Mastery = c.Values["mastery"]
			}
			if c.Active(5121015) {
				b.WATKPercent = c.Values["x"]
			}
			return b
		}})
	// 雲體風身 Counterattack: damage reduction buff via INVINCIBLE (guard skill
	// list 0x92B408; physical/magic taken-damage calc 0x7093E1 / 0x7097C0).
	skills.Register(skills.Handler{ID: 5111010, Kind: skills.KindBuff | skills.KindPassive, Cast: valueBuff("INVINCIBLE", "x"),
		Passive: func(c *skills.PassiveContext) skills.Bonus {
			if !c.Active(5111010) {
				return skills.Bonus{}
			}
			return skills.Bonus{DamageReduce: c.Values["x"]}
		}})
	// 海盜風采 Jolly Roger: same temporary stats and active damR as 5721009.
	skills.Register(skills.Handler{ID: 5221018, Kind: skills.KindBuff | skills.KindPassive,
		Cast: zenBuff(
			zenStat{"PIRATES_REVENGE", "damR"}, zenStat{"ABNORMAL_STATUS_R", "x"}, zenStat{"ELEMENTAL_STATUS_R", "y"},
			zenStat{"STANCE", "z"}, zenStat{"AVOID", "eva"},
		),
		Passive: func(c *skills.PassiveContext) skills.Bonus {
			if !c.Active(5221018) {
				return skills.Bonus{}
			}
			return skills.Bonus{DamagePercent: c.Values["damR"]}
		}})
	// Cost-only cast whose temporary stat has not been verified.
	skills.Register(skills.Handler{ID: 5211009, Kind: skills.KindSpecial, Cast: skills.CostOnly}) // 魔法彈丸
}

// Crew variants share the cast, buff and level. Their own WZ books set their
// attack strength, as with the three separate assets the client loads.
func crewSpec(variants []int) *combat.AttackSpec {
	return &combat.AttackSpec{
		Kind: combat.Summoned, LevelFrom: summonCrew,
		Summon: &combat.SummonSpec{MoveAbility: follow, AssistType: 1, Variants: variants},
		Prepare: func(c *combat.AttackContext) error {
			root := os.Getenv("MAPLEGO_WZ_ROOT")
			if id := c.SummonSkillID; id != 0 && id != c.Effect.SkillID {
				skill, err := content.SkillDefinition(root, id)
				if err != nil {
					return err
				}
				v, err := skill.Values(c.Effect.Level)
				if err != nil {
					return err
				}
				c.Effect.Damage = v["damage"]
			}
			if level := skills.EffectiveLevel(c.Character, 5220019, c.Now); level > 0 {
				command, err := content.SkillDefinition(root, 5220019)
				if err != nil {
					return err
				}
				v, err := command.Values(level)
				if err != nil {
					return err
				}
				c.Stats.DamageRate += v["v"]
			}
			return nil
		},
	}
}

// attackBuff is a self buff of padKey as weapon attack and, when accKey is
// set, that key as accuracy, for the WZ time.
func attackBuff(padKey, accKey string) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		d, e := buffTime(c)
		if e != nil {
			return p, e
		}
		stats := []skills.StatValue{{Stat: "WATK", Value: c.Value(padKey)}}
		if accKey != "" && c.Value(accKey) != 0 {
			stats = append(stats, skills.StatValue{Stat: "ACC", Value: c.Value(accKey)})
		}
		p.Self = &skills.Buff{Duration: d, Stats: stats}
		return p, nil
	}
}

// infiniteBullets (5201008) is the gun's Shadow Stars: v148 CUserLocal::FindAmmo
// (0x8EF0AA..0x8EF0E5) requires SPIRIT_CLAW > 0 and resolves the ammo item ID as
// SPIRIT_CLAW + 2329999 (so bullet 2330000 encodes as 1).
func infiniteBullets(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	d, e := buffTime(c)
	if e != nil {
		return p, e
	}
	val := 1
	bestSlot := 0
	for _, item := range c.Caster.Items {
		if item.InvType == 2 && item.Slot > 0 && item.Quantity > 0 &&
			combat.AmmoOf(item.TemplateID) == combat.AmmoBullet && item.TemplateID > 2329999 &&
			(bestSlot == 0 || item.Slot < bestSlot) {
			bestSlot = item.Slot
			val = item.TemplateID - 2329999
		}
	}
	p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: "SPIRIT_CLAW", Value: val}}}
	return p, nil
}
