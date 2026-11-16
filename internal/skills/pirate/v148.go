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
	{id: 5121016, kind: combat.Melee | combat.Shoot, noAmmo: true}, // 能量爆發
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
	passive(5110011, func(c *skills.PassiveContext) skills.Bonus { // 爆擊鬥氣: cr and both critical damage bounds
		return skills.Bonus{Critical: c.Values["cr"], CriticalDamageMin: c.Values["criticaldamageMin"], CriticalDamage: c.Values["criticaldamageMax"]}
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
	passive(5220020, func(c *skills.PassiveContext) skills.Bonus { // 槍神奧義: mastery with a gun; its extra shot is not applied
		if weaponType(c.Character) != gun {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.Values["mastery"]}
	})
	skills.Register(skills.Handler{ID: 5100013, Kind: skills.KindPassive}) // 耐久: see the server's periodic recovery
	passive(5220019, func(c *skills.PassiveContext) skills.Bonus {         // 船員指令
		if c.Active(summonCrew) {
			return skills.Bonus{WATK: c.Values["pad"]}
		}
		return skills.Bonus{}
	})

	skills.Register(skills.Handler{ID: 5101011, Kind: skills.KindBuff, Cast: attackBuff("indiePad", "acc")}) // 全神貫注
	skills.Register(skills.Handler{ID: 5201008, Kind: skills.KindBuff, Cast: infiniteBullets})               // 無形彈藥
	skills.Register(skills.Handler{ID: 5201011, Kind: skills.KindSpecial, Cast: skills.CostOnly})            // 飛翼
	skills.Register(skills.Handler{ID: 5121015, Kind: skills.KindSpecial | skills.KindPassive, Cast: skills.CostOnly,
		Passive: func(c *skills.PassiveContext) skills.Bonus {
			b := skills.Bonus{ASR: c.Values["asrR"], TER: c.Values["terR"]}
			if weaponType(c.Character) == knuckle {
				b.Mastery = c.Values["mastery"]
			}
			return b
		}})
	// Cost-only casts whose temporary stat has not been verified.
	for _, id := range []int{5111010, 5211009, 5221018} { // 雲體風身, 魔法彈丸, 海盜風采
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSpecial, Cast: skills.CostOnly})
	}
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

// infiniteBullets (5201008) is the gun's Shadow Stars: SPIRIT_CLAW 0 for the
// WZ time, and shots use no bullet while it lasts.
func infiniteBullets(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	d, e := buffTime(c)
	if e != nil {
		return p, e
	}
	p.Self = &skills.Buff{Duration: d, Stats: []skills.StatValue{{Stat: "SPIRIT_CLAW", Value: 0}}}
	return p, nil
}
