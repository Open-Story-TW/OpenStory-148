package resist2

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
)

// 狂豹獵人 3300／3310／3311／3312. Swallow itself (33101005) needs the core
// keydown/suck support and is not registered yet.

// jaguarMount is Java getMountItem(33001001) without a captured jaguar
// (quest JAGUAR record; capturing is not implemented).
const jaguarMount = 1932015

// hunterAttack is one Wild Hunter attack. Crossbow skills and jaguar ranged
// AOE skills (33101002, 33101007, 33121001) are RecvShootAttack; the jaguar
// melee skills 33111002, 33111006 and Sonic Roar 33121002 arrive as
// RecvMeleeAttack (v148 skill dispatcher 0x93694C -> melee sender 0x91A08F).
type hunterAttack struct {
	id   int
	kind combat.AttackKind
	// aoe is Java rangedAttack's AOE list: no arrow is consumed.
	aoe       bool
	onHit     []combat.MobStatusApply
	maxDamage func(combat.DamageContext) int
	drain     func(*combat.AttackContext, int64) (int, int)
	// levelFrom is Java getLinkedAranSkill: the learned skill whose level
	// authorizes this hidden attack.
	levelFrom int
	// lineDamage is the native per-line skill percent (combat.AttackSpec).
	lineDamage func(e combat.Effect, line, lines int) int
}

var (
	jaguar = combat.Melee | combat.Shoot
	// chemicalShell is Java 33121005: SHOWDOWN, MDEF and WDEF x.
	chemicalShell = []combat.MobStatusApply{
		{Stat: combat.MobShowdown, ValueFrom: "x"}, {Stat: combat.MobMDR, ValueFrom: "x"}, {Stat: combat.MobPDR, ValueFrom: "x"},
	}
)

var hunterAttacks = []hunterAttack{
	{id: 33001000, kind: combat.Shoot},                                       // 三重射 Triple Shot
	{id: 33100009, kind: combat.Shoot},                                       // 終極攻擊 Final Attack
	{id: 33101001, kind: combat.Shoot, onHit: stun, maxDamage: bombCeiling},  // 炸彈之箭 Arrow Bomb
	{id: 33101002, kind: combat.Shoot, aoe: true, onHit: stun},               // 美洲豹咆哮 Jaguar Rawr
	{id: 33101007, kind: combat.Shoot, aoe: true, levelFrom: 33101005},       // 吞食 (spit)
	{id: 33111001, kind: combat.Shoot},                                       // 瘋狂射擊 Wild Shot
	{id: 33111002, kind: combat.Melee, onHit: stun},                          // 歧路 Jaguar Kick
	{id: 33111006, kind: combat.Melee, drain: swipeDrain},                    // 爪攻擊 Swipe
	{id: 33120011, kind: combat.Shoot},                                       // 進階終極攻擊 Advanced Final Attack
	{id: 33121001, kind: combat.Shoot, aoe: true, lineDamage: explosionLine}, // 閃光雨 Exploding Arrows
	{id: 33121002, kind: combat.Melee, aoe: true, onHit: stun},               // 音爆 Sonic Roar (v148 0x93694C -> melee 0x91A08F)
	{id: 33121005, kind: combat.Shoot, onHit: chemicalShell},                 // 化學彈丸 Stinking Shot
	{id: 33121009, kind: combat.Shoot},                                       // 狂野帕爾坎 Wild Arrow Blast (keydown quirk in S0b)
}

// hunterSpec is the attack spec: Soul Arrow (an active SOULARROW) and the
// AOE skills shoot without arrows, like Java rangedAttack.
func hunterSpec(a hunterAttack) *combat.AttackSpec {
	s := &combat.AttackSpec{Kind: a.kind, OnHit: a.onHit, MaxDamage: a.maxDamage, Drain: a.drain, LevelFrom: a.levelFrom, LineDamage: a.lineDamage}
	if a.aoe {
		s.Ammo = combat.AmmoNone
	}
	s.Prepare = func(ctx *combat.AttackContext) error {
		if _, ok := skills.ActiveStat(ctx.Character, "SOULARROW", ctx.Now); ok {
			ctx.NoAmmo = true
		}
		return nil
	}
	return s
}

// explosionLine (33121001): the y arrows hit at damage% and the last line is
// the x% explosion. The v148 physical calc 0x71160B takes x for the line
// a27 == attackCount-1 of 33121001 (0x712B62).
func explosionLine(e combat.Effect, line, lines int) int {
	if line == lines-1 {
		return e.X
	}
	return 0
}

// bombCeiling (33101001) is Java DamageParse's per-line bound times mobCount.
func bombCeiling(c combat.DamageContext) int {
	return min(combat.LineCeiling(c)*max(1, c.Effect.MobCount), combat.MaxLineDamage())
}

// swipeDrain (33111006) heals x percent of the damage dealt, at most half of
// MaxHP (Java MapleCharacter afterAttack).
func swipeDrain(ctx *combat.AttackContext, dealt int64) (int, int) {
	return int(min(dealt*int64(ctx.Effect.X)/100, int64(ctx.Character.MaxHP/2))), 0
}

func init() {
	for _, a := range hunterAttacks {
		kind := skills.KindAttack
		if a.onHit != nil {
			kind |= skills.KindMobStatus
		}
		skills.Register(skills.Handler{ID: a.id, Kind: kind, Attack: hunterSpec(a)})
	}

	// Buffs.
	skills.Register(skills.Handler{ID: 33001001, Kind: skills.KindBuff | skills.KindMorph, Cast: jaguarRiding, Passive: jaguarBonus})
	skills.Register(skills.Handler{ID: 33001002, Kind: skills.KindSpecial, Cast: skills.CostOnly})                                      // 雙重跳躍 Double Jump
	skills.Register(skills.Handler{ID: 33001003, Kind: skills.KindBuff, Cast: buff(stat{"BOOSTER", "x"})})                              // 快速之弩 Crossbow Booster
	skills.Register(skills.Handler{ID: 33101003, Kind: skills.KindBuff, Cast: buff(stat{"SOULARROW", "x"})})                            // 無形之箭 Soul Arrow
	skills.Register(skills.Handler{ID: 33101004, Kind: skills.KindBuff, Cast: buff(stat{"RAINING_MINES", "x"})})                        // 地雷 Mine
	skills.Register(skills.Handler{ID: 33111004, Kind: skills.KindBuff, Cast: buff(stat{"BLIND", "x"})})                                // 黑暗狙擊 Blind
	skills.Register(skills.Handler{ID: 33121004, Kind: skills.KindPartyBuff, Cast: party(sharpEyes)})                                   // 銳利之眼 Sharp Eyes
	skills.Register(skills.Handler{ID: 33121007, Kind: skills.KindPartyBuff, Cast: party(buff(stat{"MAPLE_WARRIOR", "x"}))})            // 楓葉祝福 Maple Warrior
	skills.Register(skills.Handler{ID: 33101006, LevelFrom: 33101005, Kind: skills.KindBuff, Cast: swallowBuff, Passive: swallowBonus}) // 吞食 random buff
	skills.Register(skills.Handler{ID: 33121008, Kind: skills.KindBuff, Cast: skills.HerosWill})                                        // 楓葉淨化 Hero's Will
	skills.Register(skills.Handler{ID: 33111007, Kind: skills.KindBuff, Cast: felineBerserk, Passive: activeDamage(33111007, "y")})     // 狂獸附體 Feline Berserk

	// Passives.
	passive(33000004, func(*skills.PassiveContext) skills.Bonus { return skills.Bonus{} }) // 自然吸血術: Java has no effect
	passive(33100000, mastery(crossbow, 33120000))                                         // 精準之弩 Crossbow Mastery
	passive(33100010, physicalTraining)                                                    // 體能訓練 Physical Training
	passive(33110000, func(c *skills.PassiveContext) skills.Bonus {                        // 騎乘精通 Jaguar Mastery: Java dam_r/bossdam_r always
		return skills.Bonus{DamagePercent: c.Values["damage"], BossDamagePercent: c.Values["damage"]}
	})
	passive(33120000, func(c *skills.PassiveContext) skills.Bonus { // 弩術精通 Crossbow Expert
		b := skills.Bonus{WATK: c.Values["x"], CriticalDamageMin: c.Values["criticaldamageMin"]}
		if weaponType(c.Character) == crossbow {
			b.Mastery = c.Values["mastery"]
		}
		return b
	})
	passive(33120010, func(c *skills.PassiveContext) skills.Bonus { // 狂暴天性 Wild Instinct
		return skills.Bonus{IgnoreDefense: c.Values["ignoreMobpdpR"], AvoidRate: c.Values["er"]}
	})

	// Summons.
	skills.Register(skills.Handler{ID: 33101008, LevelFrom: 33101004, Kind: skills.KindSummon, Cast: mine, Attack: &combat.AttackSpec{Kind: combat.Summoned, MaxDamage: selfDestructCeiling, Summon: &combat.SummonSpec{MoveAbility: stationary, Multi: true, SingleAttack: true}}})
	skills.Register(skills.Handler{ID: 33111003, Kind: skills.KindSummon, Cast: summonBuff("SUMMON", false), Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 1, Puppet: true}}})
	skills.Register(skills.Handler{ID: 33111005, Kind: skills.KindSummon | skills.KindMobStatus, Cast: summonBuff("SUMMON", false), Attack: &combat.AttackSpec{Kind: combat.Summoned, OnHit: stun, Summon: &combat.SummonSpec{MoveAbility: circleFollow, AssistType: 1}}})
}

// jaguarRiding (33001001) mounts the jaguar: MONSTER_RIDING carries the
// mount item (Java giveMount), skills.Permanent, recast to dismount.
func jaguarRiding(c *skills.CastContext) (skills.CastPlan, error) {
	return mount(c, jaguarMount, nil), nil
}

// jaguarBonus is Java PlayerStats while riding the jaguar: w critical rate
// and z percent MaxHP.
func jaguarBonus(c *skills.PassiveContext) skills.Bonus {
	if buffSource(c.Character, "MONSTER_RIDING", c.Now) != 33001001 {
		return skills.Bonus{}
	}
	return skills.Bonus{Critical: c.Values["w"], MaxHPPercent: c.Values["z"]}
}

// felineBerserk (33111007): SPEED z, DAMAGE_BUFF y and ATTACK_BUFF x (Java
// statups), with the WZ cooltime.
func felineBerserk(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(stat{"SPEED", "z"}, stat{"DAMAGE_BUFF", "y"}, stat{"ATTACK_BUFF", "x"})(c)
}

// activeDamage is Java PlayerStats' DAMAGE_BUFF while skill id's buff runs:
// key percent on normal and boss damage.
func activeDamage(id int, key string) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		if !c.Active(id) {
			return skills.Bonus{}
		}
		return skills.Bonus{DamagePercent: c.Values[key], BossDamagePercent: c.Values[key]}
	}
}

// mine (33101008) is the mine the client drops while RAINING_MINES (33101004)
// runs. Java rejects it without the Mine buff or while any SUMMON buff is up.
func mine(c *skills.CastContext) (skills.CastPlan, error) {
	if buffSource(c.Caster, "RAINING_MINES", c.Now) != 33101004 {
		return skills.StandardCost(c), fmt.Errorf("skill33101008: Mine is not active")
	}
	if _, ok := skills.ActiveStat(c.Caster, "SUMMON", c.Now); ok {
		return skills.StandardCost(c), fmt.Errorf("skill33101008: a summon is active")
	}
	return summonBuff("SUMMON", false)(c)
}

// selfDestructCeiling bounds a summon that explodes for selfDestruction
// percent (mines, bots): Java SummonHandler's maxBase × 5 × damage%.
func selfDestructCeiling(c combat.DamageContext) int {
	v := skillValues(c.Effect.SkillID, c.Effect.Level)
	if v == nil {
		return 0
	}
	c.Effect.Damage = max(c.Effect.Damage, v["selfDestruction"])
	return combat.SummonCeiling(c)
}

// swallowBuffs are Java's six 33101006 outcomes and the WZ key of each value.
var swallowBuffs = []stat{
	{"CRITICAL_RATE_BUFF", "y"}, {"MP_BUFF", "y"}, {"DAMAGE_TAKEN_BUFF", "x"},
	{"DODGE_CHANGE_BUFF", "x"}, {"DAMAGE_BUFF", "y"}, {"ATTACK_BUFF", "y"},
}

// swallowBuff (33101006) is one of six buffs at random for the WZ time
// (Java MapleStatEffect case 33101006, Randomizer.nextInt(6)).
func swallowBuff(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(swallowBuffs[rollDie(len(swallowBuffs))-1])(c)
}

// swallowBonus is what Java PlayerStats reads of the swallow buffs: critical
// rate, MaxMP percent and damage; the core takes DAMAGE_TAKEN_BUFF off hits.
func swallowBonus(c *skills.PassiveContext) skills.Bonus {
	var b skills.Bonus
	for _, r := range c.Character.SkillBuffs {
		if r.ID != 33101006 || !r.ExpiresAt.After(c.Now) {
			continue
		}
		switch r.Stat {
		case "CRITICAL_RATE_BUFF":
			b.Critical += r.Value
		case "MP_BUFF":
			b.MaxMPPercent += r.Value
		case "DAMAGE_BUFF":
			b.DamagePercent += r.Value
			b.BossDamagePercent += r.Value
		}
	}
	return b
}
