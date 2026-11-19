package mercphantom

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/skills"
	"os"
	"time"
)

// Mercedes attacks. Java DamageParse parses 23001000, 23101000, 23111000,
// 23111001, 23121000 and 23121002 as ranged (WZ info type 2, plus Leaf
// Tornado's extra fields) and the rest as close range; dual bowguns shoot
// without arrows. Both families are accepted until MCP records the opcode
// each skill really sends.
const either = combat.Melee | combat.Shoot

// rising is the damR passive of Rising Rush and its follow-up. The v148
// client adds both: live crit lines of 23101001 with 23110006 and 23120011
// learned exceeded the 130+50% bound by the 23110006 share.
var rising = []int{23120011, 23110006}

var mercedesAttacks = map[int]rule{
	// 精靈遊俠 2300
	23001000: {kind: either}, // 急速雙擊 Swift Dual Shot
	// 2310
	23100004: {kind: either},                                      // 最終一擊 Stunning Strikes (after Rising Rush)
	23100006: {kind: either, mobCount: 6},                         // 終極攻擊：雙弩槍 Final Attack (Java mobCount 6)
	23101000: {kind: either},                                      // 精准光速神弩 Piercing Storm
	23101001: {kind: either, boost: rising},                       // 昇龍刺擊 Rising Rush
	23101007: {kind: either, boost: rising, levelFrom: 23101001},  // (invisible) Rising Rush kick, linked to 23101001
	23110006: {kind: either},                                      // 騰空踢擊 Aerial Barrage (WZ instant-kill prop: Java none)
	23111000: {kind: either, onHit: stun, boost: []int{23121000}}, // 光速雙擊 Lightning Edge: WZ stun at prop for time
	23111001: {kind: either, boost: []int{23121002}},              // 落葉旋風射擊 Leaf Tornado
	23111002: {kind: either, onHit: imprint},                      // 獨角獸射擊 Unicorn Spike
	23111003: {kind: either},                                      // 旋風突進 Gust Dive
	// 2312
	23120011: {kind: either},                          // 旋風月光翻轉 Rolling Moonsault
	23121000: {kind: either},                          // 伊修塔爾之環 Ishtar's Ring
	23121002: {kind: either, onHit: spikes},           // 傳說之槍 Spikes Royale
	23121003: {kind: either, maxDamage: snipeCeiling}, // 閃電之鋒 Lightning Edge (Java: non-boss up to max HP, always critical)
}

var (
	stun = []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1}}
	// imprint is Java MonsterStatus.IMPRINT (native 34) = x: targets take
	// x% more damage from everyone (see imprintCeiling).
	imprint = []combat.MobStatusApply{{Stat: combat.MobWeakness, ValueFrom: "x"}}
	// spikes is Java WDEF -x (x is already negative in WZ) for the WZ time.
	spikes = []combat.MobStatusApply{{Stat: combat.MobPDR, ValueFrom: "x"}}
)

func init() {
	for id, r := range mercedesAttacks {
		register(id, r)
	}

	passive(23000001, func(c *skills.PassiveContext) skills.Bonus { // 潛在力量
		return skills.Bonus{AvoidRate: c.Values["er"], Speed: c.Values["psdSpeed"], SpeedLimitBonus: c.Values["speedMax"]}
	})
	passive(23000003, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{Critical: c.Values["cr"]} }) // 鋒利瞄準 Sharp Aiming
	passive(23000004, nil)                                                                                           // 內查斯特雷音 link skill: Java pvpSkills, no PvE effect
	passive(23100005, weaponMastery(dualBowgun, 23120009))                                                           // 雙弩槍精通 Dual Bowguns Mastery
	passive(23100008, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
	}) // 體能訓練 Physical Training
	passive(23120009, expertMastery(dualBowgun))                                                                         // 進階雙弩槍精通 Dual Bowguns Expert
	passive(23120010, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{IgnoreDefense: c.Values["x"]} }) // 破防射擊 Defense Break (Java ignoreTargetDEF += x)

	skills.Register(skills.Handler{ID: 23120012, Kind: skills.KindAttack | skills.KindPassive, Attack: attack(rule{kind: either, mobCount: 6}), Passive: advancedFinalAttack})         // 進階終極攻擊 Advanced Final Attack
	skills.Register(skills.Handler{ID: 23001002, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                                                                     // 藝術跳躍 Glide Blast
	skills.Register(skills.Handler{ID: 23101002, Kind: skills.KindBuff, Cast: buff(dualBowgun, key("BOOSTER", "x"))})                                                                  // 雙弩槍推進器 Dual Bowguns Boost
	skills.Register(skills.Handler{ID: 23101003, Kind: skills.KindBuff, Cast: buff(0, key("SPIRIT_SURGE", "x")), Passive: spiritSurge})                                                // 靈魂灌注 Spirit Surge
	skills.Register(skills.Handler{ID: 23111004, Kind: skills.KindBuff, Cast: buff(0, key("ANGEL_ATK", "indiePad")), Passive: dodge})                                                  // 依古尼斯咆哮 Ignis Roar
	skills.Register(skills.Handler{ID: 23111005, Kind: skills.KindBuff, Cast: buff(0, key("ABNORMAL_STATUS_R", "terR"), key("ELEMENTAL_STATUS_R", "terR"), key("WATER_SHIELD", "x"))}) // 水之盾 Water Shield
	skills.Register(skills.Handler{ID: 23121004, Kind: skills.KindBuff, Cast: buff(0, key("PIRATES_REVENGE", "damR"), key("ENHANCED_MAXHP", "emhp")), Passive: ancientWarding})        // 遠古意志 Ancient Warding
	skills.Register(skills.Handler{ID: 23121005, Kind: skills.KindPartyBuff, Cast: partyBuff(key("MAPLE_WARRIOR", "x"))})                                                              // 楓葉祝福 Maple Warrior
	skills.Register(skills.Handler{ID: 23121008, Kind: skills.KindSpecial, Cast: skills.HerosWill})                                                                                    // 勇士的意志 Hero's Will

	skills.Register(skills.Handler{ID: 23111008, Kind: skills.KindSummon | skills.KindMobStatus, Cast: knightCast, Attack: knightSpec(knightVariants)}) // 元素騎士 Elemental Knights
	for _, id := range knightVariants[1:] {                                                                                                             // shown variants, never cast
		skills.Register(skills.Handler{ID: id, Kind: skills.KindSummon | skills.KindMobStatus, LevelFrom: 23111008, Attack: knightSpec(nil)})
	}

	combat.ModifyCeiling(imprintCeiling)
}

// advancedFinalAttack (23120012) is Java watk += padX; ar is the accuracy
// rate percent.
func advancedFinalAttack(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{WATK: c.Values["padX"], ACCPercent: c.Values["ar"]}
}

// spiritSurge (23101003) is Java PlayerStats while SPIRIT_SURGE is up:
// critical rate +x and damage (and boss damage) ×(100+x)%.
func spiritSurge(c *skills.PassiveContext) skills.Bonus {
	if !c.Active(23101003) {
		return skills.Bonus{}
	}
	return skills.Bonus{Critical: c.Values["x"], DamagePercent: c.Values["x"], BossDamagePercent: c.Values["x"]}
}

// dodge is the passive avoid chance of Ignis Roar (23111004) and Ancient
// Warding (23121004): Java dodgeChance += prop. (Legacy reads 23110004 for
// Ignis Roar, a v145 ID that does not exist; the cast ID is used here.)
func dodge(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{AvoidRate: c.Values["prop"]} }

// ancientWarding (23121004) adds dodge and, while its PIRATES_REVENGE buff is
// up, Java PlayerStats dam_r and bossdam_r ×(100+damR)%.
func ancientWarding(c *skills.PassiveContext) skills.Bonus {
	b := dodge(c)
	if c.Active(23121004) {
		b.DamagePercent, b.BossDamagePercent = c.Values["damR"], c.Values["damR"]
	}
	return b
}

// imprintCeiling raises every attacker's ceiling on a monster under Unicorn
// Spike's IMPRINT by its value percent (WZ: "受到我軍所有攻擊的追加傷害";
// Legacy only applies the status). The hit-count limit (y, doubled on bosses)
// is not tracked; the ceiling is an upper bound, so the bonus stays until the
// status expires.
func imprintCeiling(c combat.DamageContext, ceiling int) int {
	s := c.Statuses[combat.MobWeakness]
	if s == nil || s.SkillID != 23111002 || s.Value <= 0 {
		return ceiling
	}
	return int(min(int64(ceiling)*int64(100+s.Value)/100, int64(combat.MaxLineDamage())))
}

// Elemental Knights (Java MapleSummon: CIRCLE_FOLLOW, attacking). The player
// learns and casts 23111008; the S0b spawn shows one of 23111008/9/10 at
// random (SummonSpec.Variants; Java's nextInt(2) never reached 23111010)
// while spec, level and cost stay 23111008's. Its WZ level holds all three
// knights, so Prepare picks by the attacking variant (AttackContext
// SummonSkillID):
//   - 23111008 冷氣精靈: damage%, freezes at prop for subTime (Java STUN 1).
//   - 23111009 火焰精靈: v%, fire, burns y% every x s for w s.
//   - 23111010 黑暗精靈: z%.
var knightVariants = []int{23111008, 23111009, 23111010}

func knightSpec(variants []int) *combat.AttackSpec {
	return &combat.AttackSpec{Kind: combat.Summoned, Prepare: knight, Summon: &combat.SummonSpec{MoveAbility: 4, AssistType: 1, Variants: variants}}
}

func knight(ctx *combat.AttackContext) error {
	e := &ctx.Effect
	def, err := content.SkillDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), 23111008)
	if err != nil {
		return err
	}
	v, err := def.Values(min(max(e.Level, 1), def.MaxLevel))
	if err != nil {
		return err
	}
	switch ctx.SummonSkillID {
	case 23111008:
		e.MonsterStatus = append(e.MonsterStatus, combat.MobStatusApply{Stat: combat.MobFreeze, Value: 1, SubTime: true})
	case 23111009:
		e.Damage, e.Element, e.Prop = v["v"], combat.Fire, 0
		e.DOT, e.DOTTime, e.DOTInterval = v["y"], time.Duration(v["w"])*time.Second, time.Duration(v["x"])*time.Second
		e.MonsterStatus = append(e.MonsterStatus, combat.MobStatusApply{Stat: combat.MobPoison, Value: 1, Poison: true, Duration: e.DOTTime})
	case 23111010:
		e.Damage = v["z"]
	default: // unknown variant: the strongest knight's ceiling, no status
		e.Damage = max(v["damage"], v["v"], v["z"])
	}
	return nil
}

// knightCast is Java SUMMON 1 for the WZ time with mpCon and cooltime.
func knightCast(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("time") < 1 {
		return p, fmt.Errorf("skill%d: no duration", c.Skill.ID)
	}
	p.Self = &skills.Buff{Duration: time.Duration(c.Value("time")) * time.Second, Stats: []skills.StatValue{{Stat: "SUMMON", Value: 1}}}
	return p, nil
}
