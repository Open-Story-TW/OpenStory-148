package common

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"time"
)

// beginnerPassives registers the always-on beginner skills. Java PlayerStats
// only reads some of them; the rest are markers (KindPassive without a
// Passive) so they can be learned and kept but add nothing, as in Legacy.
func beginnerPassives() {
	// No Java PlayerStats effect: 寵物達人, 游泳達人, 收納達人, 神匠之魂 (read by
	// the scroll path), 聯盟的意志, 效能 outside Resistance, 一擊 outside
	// Mercedes/Resistance, 找回的記憶, 王的資格 (charm trait only), Phantom's
	// 20031204/20031206 and 魔族之血 (will/charisma traits).
	for _, suffix := range []int{1003, 190} {
		each(suffix, skills.Handler{Kind: skills.KindPassive})
	}
	for _, id := range []int{
		8, 10000018, 20000024, 20011024, 20020213, 30001024, 109, 111, 112,
		20020002, 20030002, 30010002, 20030022, 30010022, 20000194, 20020112, 20031204, 20031206, 30010185,
	} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive})
	}

	// 百烈祝福 (110): all stats strX.. and MaxHP/MP mhpR/mmpR (Java adds the
	// stats for Explorers and the percentages for everyone; only Explorers
	// learn 110).
	skills.Register(skills.Handler{ID: 110, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		v := c.Values
		return skills.Bonus{STR: v["strX"], DEX: v["dexX"], INT: v["intX"], LUK: v["lukX"], MaxHPPercent: v["mhpR"], MaxMPPercent: v["mmpR"]}
	}})
	// 百烈祝福 (80000000): Java adds only the stats (its HP/MP part is
	// commented out).
	skills.Register(skills.Handler{ID: 80000000, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus {
		v := c.Values
		return skills.Bonus{STR: v["strX"], DEX: v["dexX"], INT: v["intX"], LUK: v["lukX"]}
	}})
	skills.Register(skills.Handler{ID: 80000001, Kind: skills.KindPassive, Passive: func(c *skills.PassiveContext) skills.Bonus { // 後續待發
		return skills.Bonus{BossDamagePercent: c.Values["bdR"]}
	}})
	// 繼承的意志 (20010194) and 女皇的傲氣 (10000074): Legacy hard-codes
	// MaxHP +25% and MaxHP/MP +20% instead of the WZ values.
	skills.Register(skills.Handler{ID: 20010194, Kind: skills.KindPassive, Passive: func(*skills.PassiveContext) skills.Bonus {
		return skills.Bonus{MaxHPPercent: 25}
	}})
	skills.Register(skills.Handler{ID: 10000074, Kind: skills.KindPassive, Passive: func(*skills.PassiveContext) skills.Bonus {
		return skills.Bonus{MaxHPPercent: 20, MaxMPPercent: 20}
	}})
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
