package pirate

import (
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
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

var v148Summons = []summon{
	{id: 5211011, stat: "SUMMON", move: follow},                  // 召喚船員
	{id: 5211015, stat: "SUMMON", move: follow},                  // 召喚船員
	{id: 5211016, stat: "SUMMON", move: follow},                  // 召喚船員
	{id: 5211014, stat: "PUPPET", move: stationary, multi: true}, // 砲台章魚王
}

func init() {
	for _, a := range v148Attacks {
		skills.Register(skills.Handler{ID: a.id, Kind: skills.KindAttack, Attack: spec(a)})
	}
	for _, s := range v148Summons {
		spec := &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: s.move, AssistType: 1, Multi: s.multi}}
		skills.Register(skills.Handler{ID: s.id, Kind: skills.KindSummon, Cast: valueOne(s.stat), Attack: spec})
	}
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
	passive(5120014, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{IgnoreDefense: c.Values["x"]} }) // 防禦撞擊: x
	passive(5210012, func(c *skills.PassiveContext) skills.Bonus {                                                      // 神槍手耐性
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
	skills.Register(skills.Handler{ID: 5100013, Kind: skills.KindPassive}) // 耐久: recovery over time, not applied
	skills.Register(skills.Handler{ID: 5220019, Kind: skills.KindPassive}) // 船員指令: bonuses per summoned crew, not applied

	for _, id := range []int{5120012, 5220014} { // 雙倍幸運骰子
		skills.Register(skills.Handler{ID: id, Kind: skills.KindBuff, Cast: doubleDice})
	}
	skills.Register(skills.Handler{ID: 5101011, Kind: skills.KindBuff, Cast: attackBuff("indiePad", "acc")}) // 全神貫注
	skills.Register(skills.Handler{ID: 5201008, Kind: skills.KindBuff, Cast: infiniteBullets})               // 無形彈藥
	skills.Register(skills.Handler{ID: 5201011, Kind: skills.KindSpecial, Cast: skills.CostOnly})            // 飛翼
	// Cost only: what these buffs give has no buff stat here yet.
	for _, id := range []int{5111010, 5121015, 5211009, 5221018} { // 雲體風身, 拳霸大師, 魔法彈丸, 海盜風采
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSpecial, Cast: skills.CostOnly})
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
