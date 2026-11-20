package common

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

// beginnerPassives registers beginner skills and their permanent bonuses.
func beginnerPassives() {
	// No Java PlayerStats effect: 寵物達人, 游泳達人, 收納達人, 神匠之魂 (read by
	// the scroll path), 效能 outside Resistance, 一擊 outside
	// Mercedes/Resistance, 找回的記憶, 王的資格 (charm trait only), Phantom's
	// 20031204/20031206 and 魔族之血 (will/charisma traits).
	each(1003, skills.Handler{Kind: skills.KindPassive})
	// 聯盟的意志 grants four base attributes and both attack stats in v148.
	willOfAlliance := skills.Handler{Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		v := c.Values
		return skills.Bonus{STR: v["strX"], DEX: v["dexX"], INT: v["intX"], LUK: v["lukX"], WATK: v["padX"], MATK: v["madX"]}
	}}
	each(190, willOfAlliance)
	for _, id := range []int{
		8, 10000018, 20000024, 20011024, 20020213, 30001024, 109, 111, 112,
		20020002, 20030002, 30010002, 20030022, 30010022, 20031204, 20031206, 30010185,
	} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive})
	}

	// Both v148 百烈祝福 forms grant all four attributes and WZ HP/MP percentages.
	for _, id := range []int{110, 80000000} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
			v := c.Values
			return skills.Bonus{STR: v["strX"], DEX: v["dexX"], INT: v["intX"], LUK: v["lukX"], MaxHPPercent: v["mhpR"], MaxMPPercent: v["mmpR"]}
		}})
	}
	skills.Register(skills.Handler{ID: 80000001, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus { // 後續待發
		return skills.Bonus{BossDamagePercent: c.Values["bdR"]}
	}})
	// v148 maps Phantom's 致命本能 to the linked form with the same critical bonus.
	for _, id := range []int{20030204, 80000002} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{Critical: c.Values["cr"]}
		}})
	}
	skills.Register(skills.Handler{ID: 80000016, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		v := c.Values
		return skills.Bonus{STR: v["strX"], DEX: v["dexX"], INT: v["intX"], LUK: v["lukX"],
			WATK: v["padX"], MATK: v["madX"], IgnoreDefense: v["ignoreMobpdpR"]}
	}})
	// 獵人資格證 (見習/B/A/S) and 80000137 add the attributes, vitals,
	// attack and defense their v148 WZ lists.
	for _, id := range []int{80000133, 80000134, 80000135, 80000136, 80000137} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
			v := c.Values
			return skills.Bonus{STR: v["strX"], DEX: v["dexX"], INT: v["intX"], LUK: v["lukX"],
				WATK: v["padX"], MATK: v["madX"], MaxHP: v["mhpX"], MaxMP: v["mmpX"], WDEF: v["pddX"], MDEF: v["mddX"]}
		}})
	}
	// 找回的記憶 (Aran 20000194): the v148 WZ grants attack, defense, speed,
	// critical rate and boss damage.
	skills.Register(skills.Handler{ID: 20000194, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		v := c.Values
		return skills.Bonus{WATK: v["padX"], WDEF: v["pddX"], Speed: v["psdSpeed"], Critical: v["cr"], BossDamagePercent: v["bdR"]}
	}})
	// 草上飛 (228): movement speed, its speed cap and jump from the v148 WZ.
	skills.Register(skills.Handler{ID: 228, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		v := c.Values
		return skills.Bonus{Speed: v["psdSpeed"], Jump: v["psdJump"], SpeedLimitBonus: v["speedMax"]}
	}})
	// 王的資格 (Mercedes 20020112): movement speed and jump; its action speed
	// is applied by the client.
	skills.Register(skills.Handler{ID: 20020112, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{Speed: c.Values["psdSpeed"], Jump: c.Values["psdJump"]}
	}})
	// 繼承的意志 grants the v148 permanent bonuses from the Evan quest.
	skills.Register(skills.Handler{ID: 20010194, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		v := c.Values
		return skills.Bonus{STR: v["strX"], DEX: v["dexX"], INT: v["intX"], LUK: v["lukX"],
			MATK: v["madX"], MDEF: v["mddX"], MaxHPPercent: v["mhpR"], BossDamagePercent: v["bdR"]}
	}})
	for _, id := range []int{10000074, 50000074} { // v148 Cygnus/Mihile Empress vitals passives
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
			return skills.Bonus{MaxHPPercent: c.Values["x"], MaxMPPercent: c.Values["x"]}
		}})
	}
	// 惡魔之怒 (30010112): boss damage bdR; Java's Demon Force regeneration
	// part (mpRecover x) waits for the Demon Force resource.
	skills.Register(skills.Handler{ID: 30010112, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{BossDamagePercent: c.Values["bdR"]}
	}})
	// 效能 (30000002): Java RecoveryUP += x-100 for Resistance jobs, the
	// potion bonus (alchemistModifyVal).
	skills.Register(skills.Handler{ID: 30000002, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		if !domain.IsResistanceJob(c.Character.Job) {
			return skills.Bonus{}
		}
		return skills.Bonus{PotionPercent: c.Values["x"] - 100}
	}})
	// 一擊／會心一擊 (20020022 Mercedes, 30000022 Resistance): critical rate
	// prop and minimum critical damage (Java CalcPassive_SharpEye).
	crit := func(family func(int) bool) func(*skills.PassiveContext) skills.Bonus {
		return func(c *skills.PassiveContext) skills.Bonus {
			if !family(c.Character.Job) {
				return skills.Bonus{}
			}
			return skills.Bonus{Critical: c.Values["prop"], CriticalDamageMin: c.Values["criticaldamageMin"]}
		}
	}
	skills.Register(skills.Handler{ID: 20020022, Kind: skills.KindPassive, Passive: crit(domain.IsMercedesJob)})

	// 精靈的回復 (20020109): Mercedes recover x% of MaxHP and MaxMP every 4 s
	// (Java PlayerStats regeneration, hp/mpRecoverTime 4000).
	skills.Register(skills.Handler{ID: 20020109, Kind: skills.KindPassive | skills.KindHeal, TickEvery: 4 * time.Second, OnTick: func(c *skills.TriggerContext) *skills.BuffChange {
		if !domain.IsMercedesJob(c.Character.Job) {
			return nil
		}
		x := c.Values["x"]
		return &skills.BuffChange{HealHP: c.Character.MaxHP * x / 100, HealMP: c.Character.MaxMP * x / 100}
	}})
	// 死亡詛咒 (30010111): on a Demon Slayer's attack, a prop% chance (Java
	// nextInt(100) <= prop) to recover x% of the damage dealt, at most half
	// of MaxHP (MapleCharacter.onAttack hpRecoverPercent).
	skills.Register(skills.Handler{ID: 30010111, Kind: skills.KindPassive | skills.KindHeal, OnAttack: func(c *skills.TriggerContext) *skills.BuffChange {
		if c.Attack == nil || c.Attack.Damage <= 0 || !domain.IsDemonJob(c.Character.Job) || rand.IntN(100) > c.Values["prop"] {
			return nil
		}
		hp := min(c.Attack.Damage*int64(c.Values["x"])/100, int64(c.Character.MaxHP/2))
		if hp <= 0 {
			return nil
		}
		return &skills.BuffChange{HealHP: int(hp)}
	}})
	skills.Register(skills.Handler{ID: 30000022, Kind: skills.KindPassive, Passive: crit(domain.IsResistanceJob)})
}
