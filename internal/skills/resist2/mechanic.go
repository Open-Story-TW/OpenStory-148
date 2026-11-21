package resist2

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/skills"
	"time"
)

// 機甲戰神 3500／3510／3511／3512. Open Portal GX-9 (35101005) is a world
// field object (FieldOpenGate, COpenGatePool in the v148 client) and Magnetic
// Field (35111002) a three-coil summon triangle (world tesla.go).

const (
	mechMount = 1932016 // Java getMountItem(35001002/35120000)

	extremeMech   = 35120000 // 合金盔甲終極
	metalFist     = 35110014 // 金屬拳精通
	satelliteSafe = 35121006 // 終極賽特拉特
	robotMastery  = 35120001 // 機器人精通
	heavyWeapon   = 35100008 // 重裝武器精通
	siege2        = 35121013 // 合金盔甲: 重機槍 (Missile Tank)
	missileTank   = 35121005 // 合金盔甲: 導彈罐
	// The v148 operator releases at 2790+2520 ms (0x930A9B). Retain
	// enough time for the client's removal packet through frame delay.
	sg88Lifetime = 6 * time.Second

	// recoveryTick is Java MapleCharacter's recovery period (canRecover:
	// 5 seconds), which also charges Missile Tank's upkeep.
	recoveryTick = 5 * time.Second
)

// mechAttack is one mechanic attack. Java rangedAttack never consumes
// bullets for jobs 3500–3512 (noBullet), so every spec is AmmoNone. Gun
// skills and the keydown flamethrowers Java parses in parseRangedAttack are
// RecvShootAttack; Java has no rule for the mech melee-looking skills, so
// both families are accepted until MCP shows the opcode.
type mechAttack struct {
	id      int
	kind    combat.AttackKind
	onHit   []combat.MobStatusApply
	prepare func(*combat.AttackContext) error
	// levelFrom is Java getLinkedAranSkill: the learned skill whose level
	// authorizes this hidden attack.
	levelFrom int
}

var mechAttacks = []mechAttack{
	{id: 35001001, kind: combat.Shoot},                                                                // 火焰發射 Flame Launcher (keydown quirk in S0b)
	{id: 35001003, kind: jaguar, prepare: metalFistBoost},                                             // 鑽孔衝刺 ME-07 Drillhands
	{id: 35001004, kind: combat.Shoot},                                                                // 加格林射擊 Gatling Gun
	{id: 35101003, kind: jaguar, onHit: stun, prepare: metalFistBoost},                                // 自動錘 Atomic Hammer
	{id: 35101004, kind: jaguar},                                                                      // 火箭極速 Rocket Booster
	{id: 35101009, kind: combat.Shoot, prepare: heavyWeaponCrit(flamePoison), levelFrom: heavyWeapon}, // 強化的火焰發射
	{id: 35101010, kind: combat.Shoot, prepare: heavyWeaponCrit(nil), levelFrom: heavyWeapon},         // 強化的加格林
	{id: 35111015, kind: combat.Shoot, onHit: stun},                                                   // 火箭衝擊 Rocket Punch
	{id: 35121012, kind: jaguar},                                                                      // 雷射衝擊波 Laser Blast
	{id: 35111004, kind: combat.Shoot, prepare: siegeLines},                                           // 重機槍 Siege Mode attack
	{id: 35121005, kind: combat.Shoot, prepare: siegeLines},                                           // 導彈罐 Missile Tank attack
	{id: 35121013, kind: combat.Shoot, prepare: siegeLines, levelFrom: 35111004},                      // 重機槍 Siege Mode 2 attack
}

// heavyWeaponCrit gives the enhanced Flame Launcher and Gatling (35101009,
// 35101010) Heavy Weapon Mastery's cr, which String.wz names as their added
// critical rate. The v148 client takes 35100008's level data for those two
// attacks (cost 0x6DAD78, range 0x6D73DD); live, 400+ of their critical
// lines in one run exceeded the audit at the 10% rate without it.
func heavyWeaponCrit(next func(*combat.AttackContext) error) func(*combat.AttackContext) error {
	return func(ctx *combat.AttackContext) error {
		if v := skillValues(heavyWeapon, learned(ctx.Character, heavyWeapon)); v != nil {
			ctx.Effect.CriticalRate += v["cr"]
		}
		if next != nil {
			return next(ctx)
		}
		return nil
	}
}

// flamePoison (35101009) is Java isPoison (dot and dotTime): POISON for
// max(time, dotTime).
func flamePoison(ctx *combat.AttackContext) error {
	ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobPoison, Poison: true, Duration: max(ctx.Effect.Time, ctx.Effect.DOTTime)})
	return nil
}

// siegeEffect is Java's mech mode attack: MapleStatEffect sets attackCount 6
// and isNoDelaySkill mobCount 6; rangedAttack allows 15 bullets (AOE).
// Applied via LearnedEffect so both combat planning and persistence combat
// policy see the same 6-line / 6-target bounds for 35111004, 35121005 and 35121013.
func siegeEffect(_ domain.Character, _ time.Time, e *combat.Effect) error {
	e.MobCount = max(e.MobCount, 6)
	e.AttackCount = max(e.AttackCount, 6)
	e.BulletCount = max(e.BulletCount, 15)
	return nil
}

func siegeLines(ctx *combat.AttackContext) error {
	return siegeEffect(ctx.Character, ctx.Now, &ctx.Effect)
}

// sg88Body handles the operator's automatic hits; the robot's separate 0x120
// still uses its full summon attack effect. v148 0x712AC2 reads level y for
// the operator, and 0x929946 builds the common body rectangle.
func sg88Body(ctx *combat.AttackContext) error {
	if ctx.Kind != combat.Body {
		return nil
	}
	e := &ctx.Effect
	if e.Y < 1 || !e.Box.Set {
		return combat.ErrRejected
	}
	e.Damage, e.MobCount, e.AttackCount = e.Y, 1, 1
	ctx.Attack.X, ctx.Attack.Y = int16(ctx.Character.X), int16(ctx.Character.Y)
	lt, rb := e.Box.At(combat.Point{X: ctx.Character.X, Y: ctx.Character.Y}, ctx.Attack.FacingLeft())
	for _, target := range ctx.Targets {
		if target.X+target.HalfWidth < lt.X || target.X-target.HalfWidth > rb.X || target.Y < lt.Y || target.Y-target.BodyHeight > rb.Y {
			return combat.ErrRange
		}
	}
	return nil
}

func sg88Cast(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Self = &skills.Buff{Duration: sg88Lifetime, Stats: []skills.StatValue{{Stat: "SUMMON", Value: 1}}}
	return p, nil
}

// metalFistBoost adds Metal Fist Mastery's damR to 35001003 and 35101003.
// In v148 CUserLocal::UpdatePassiveSkillData (0x91667E), 35110014 has psd=1
// with psdSkill={35001003, 35101003} and applies whenever learned without a
// job gate; PDamage (0x71160B at 0x71181a/0x7127b6) multiplies by (100+damR)%.
func metalFistBoost(ctx *combat.AttackContext) error {
	if v := skillValues(metalFist, learned(ctx.Character, metalFist)); v != nil {
		ctx.Effect.Damage = ctx.Effect.Damage * (100 + v["damR"]) / 100
	}
	return nil
}

// boostedSummon is SummonCeiling with Java's damageIncrease from a passive:
// the skill damage (or selfDestruction) grows by the passive's key percent.
func boostedSummon(passiveID int, key string) func(combat.DamageContext) int {
	return func(c combat.DamageContext) int {
		if v := skillValues(c.Effect.SkillID, c.Effect.Level); v != nil {
			c.Effect.Damage = max(c.Effect.Damage, v["selfDestruction"])
		}
		if v := skillValues(passiveID, learned(c.Attacker, passiveID)); v != nil {
			c.Effect.Damage = c.Effect.Damage * (100 + v[key]) / 100
		}
		return combat.SummonCeiling(c)
	}
}

func init() {
	for _, a := range mechAttacks {
		kind := skills.KindAttack
		if a.onHit != nil || a.prepare != nil {
			kind |= skills.KindMobStatus
		}
		spec := &combat.AttackSpec{Kind: a.kind, Ammo: combat.AmmoNone, OnHit: a.onHit, Prepare: a.prepare, LevelFrom: a.levelFrom}
		h := skills.Handler{ID: a.id, Kind: kind, Attack: spec, LevelFrom: a.levelFrom}
		switch a.id {
		case 35111004, 35121013, 35121005:
			h.Kind |= skills.KindBuff | skills.KindMorph
			h.Cast = mechMode
			h.Passive = mechCritical(a.id)
			spec.LearnedEffect = siegeEffect
		}
		if a.id == siege2 {
			h.OnEnd = siege2End
		}
		if a.id == 35111004 || a.id == 35121013 {
			spec.NoCooldown = true // Java: the mode's own attack starts no cooltime.
		}
		if a.id == 35111004 {
			spec.Remap = siegeInTank
		}
		if a.id == missileTank {
			h.OnTick, h.TickEvery = missileUpkeep, recoveryTick
		}
		skills.Register(h)
	}

	// Buffs.
	skills.Register(skills.Handler{ID: 35001002, Kind: skills.KindBuff | skills.KindMorph, Cast: mechRiding, OnTick: missileUpkeep, TickEvery: recoveryTick}) // 合金盔甲: 原型 Mech: Prototype
	// 開放通道：GX-9 Open Portal: a pair of gates on the map (FieldOpenGate).
	skills.Register(skills.Handler{ID: 35101005, Kind: skills.KindFieldObject, Cast: skills.CostOnly,
		Attack: &combat.AttackSpec{Field: &combat.FieldSpec{Kind: combat.FieldOpenGate}}})
	skills.Register(skills.Handler{ID: 35101006, Kind: skills.KindBuff, Cast: buff(stat{"BOOSTER", "x"})})                                     // 機甲戰神極速 Mechanic Rage
	skills.Register(skills.Handler{ID: 35101007, Kind: skills.KindBuff, Cast: permanent(stat{"PERFECT_ARMOR", "x"}), OnDamaged: perfectArmor}) // 全備型盔甲 Perfect Armor
	skills.Register(skills.Handler{ID: 35111013, Kind: skills.KindBuff, Cast: dice})                                                           // 幸運骰子 Lucky Dice
	skills.Register(skills.Handler{ID: satelliteSafe, Kind: skills.KindBuff, Cast: permanent(stat{"SATELLITESAFE_PROC", "x"}, stat{"SATELLITESAFE_ABSORB", "y"})})
	skills.Register(skills.Handler{ID: 35121007, Kind: skills.KindPartyBuff, Cast: party(buff(stat{"MAPLE_WARRIOR", "x"}))}) // 楓葉祝福 Maple Warrior
	skills.Register(skills.Handler{ID: 35121008, Kind: skills.KindBuff, Cast: skills.HerosWill})                             // 楓葉淨化 Hero's Will

	// Passives.
	passive(35000005, func(*skills.PassiveContext) skills.Bonus { return skills.Bonus{} })    // 機遇攻擊: Java has no effect
	passive(35100000, mechanicMastery)                                                        // 機甲戰神精通 Mechanic Mastery
	passive(heavyWeapon, func(*skills.PassiveContext) skills.Bonus { return skills.Bonus{} }) // 重裝武器精通: level source of 35101009/35101010
	passive(35100011, physicalTraining)                                                       // 體能訓練 Physical Training
	passive(extremeMech, func(c *skills.PassiveContext) skills.Bonus { // 合金盔甲終極: v148 0x6D9490 applies gun mastery for all 35xx jobs; its enhanced stats ride 35001002
		if c.Character.Job/100 != 35 || weaponType(c.Character) != gun {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.Values["mastery"]}
	})
	// Metal Fist Mastery and Robot Mastery are applied in the attack and
	// summon specs (Java damageIncrease, BuffUP_Summon).
	skills.Register(skills.Handler{ID: metalFist, Kind: skills.KindPassive})
	skills.Register(skills.Handler{ID: robotMastery, Kind: skills.KindPassive})

	// Summons.
	satellite := func(id, from int) skills.Handler {
		return skills.Handler{ID: id, LevelFrom: from, Kind: skills.KindSummon, Cast: summonBuff("PUPPET", true), OnDamaged: satelliteSafety, Attack: &combat.AttackSpec{Kind: combat.Summoned, MaxDamage: boostedSummon(satelliteSafe, "damR"), Summon: &combat.SummonSpec{MoveAbility: follow, AssistType: 3, Duration: skills.Permanent}}}
	}
	// 磁場 Magnetic Field (Tesla Coil): up to three stationary coils; the third
	// forms the triangle (world tesla.go). The client picks the targets inside
	// its own triangle, so the line range is not checked against a box.
	skills.Register(skills.Handler{ID: 35111002, Kind: skills.KindSummon, Cast: summonBuff("SUMMON", false), Attack: &combat.AttackSpec{
		Kind: combat.Summoned, IgnoreRange: true, Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 1, Multi: true}}})
	skills.Register(satellite(35111001, 0)) // 賽特拉特 Satellite
	skills.Register(satellite(35111009, 35111001))
	skills.Register(satellite(35111010, 35111001)) // 衛星
	skills.Register(skills.Handler{ID: 35111005, Kind: skills.KindSummon | skills.KindMobStatus, Cast: summonBuff("SUMMON", false), Attack: &combat.AttackSpec{
		Summon:  &combat.SummonSpec{MoveAbility: stationary, AssistType: 1},
		MobCast: &combat.MobCastSpec{WholeField: true, SkipBoss: true, Statuses: []combat.MobStatusApply{{Stat: combat.MobSpeed, ValueFrom: "x"}, {Stat: combat.MobPDR, ValueFrom: "y"}}},
	}}) // 加速器 : EX-7 Acceleration Bot
	skills.Register(skills.Handler{ID: 35111011, Kind: skills.KindSummon | skills.KindHeal, Cast: summonBuff("SUMMON", false), Attack: &combat.AttackSpec{Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 1, Sub: healingRobot}}}) // 治療機器人 : H-LX Healing Robot
	// A new visit has no operator activation to resume, even when other buffs persist.
	skills.Register(skills.Handler{ID: 35121003, Kind: skills.KindSummon | skills.KindAttack, Cast: sg88Cast,
		DropOnEnter: func(domain.Character) bool { return true },
		Attack: &combat.AttackSpec{Kind: combat.Summoned | combat.Body, Prepare: sg88Body,
			Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 6, Duration: sg88Lifetime}}}) // 戰鬥機器 : 巨人錘 Giant Robot SG-88
	skills.Register(skills.Handler{ID: 35121009, Kind: skills.KindSummon, Cast: summonBuff("SUMMON", false), Attack: &combat.AttackSpec{Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 5, Sub: robotFactory}}}) // 機器人工廠 : RM1 Bots 'n Tots
	// v148 CSummoned passes death mode 1 at 0x73914D, which 0x72C068 maps
	// to the terminal 0x120 action 0x11. Other attacks from AF-11 are invalid.
	skills.Register(skills.Handler{ID: 35121010, Kind: skills.KindSummon | skills.KindBuff, Cast: amplifier, Attack: &combat.AttackSpec{Kind: combat.Summoned, Summon: &combat.SummonSpec{MoveAbility: stationary, AssistType: 1, ExpiryAction: 0x11}}})
	// v148 CSummoned fires 35121011 self-destruction at expiry (5s) with terminal
	// 0x120 action 0x11 (or 0x91 = 0x80|0x11 when facing left).
	skills.Register(skills.Handler{ID: 35121011, LevelFrom: 35121009, Kind: skills.KindSummon, Attack: &combat.AttackSpec{Kind: combat.Summoned, LevelFrom: 35121009, MaxDamage: boostedSummon(robotMastery, "x"), Summon: &combat.SummonSpec{MoveAbility: walkStationary, Multi: true, SingleAttack: true, ExpiryAction: 0x11}}}) // Bots 'n Tots bots (spawned by 35121009)
}

// permanent is a self buff of the given stats for skills.Permanent (Java
// duration 2100000000); recasting cancels it.
func permanent(stats ...stat) func(*skills.CastContext) (skills.CastPlan, error) {
	return func(c *skills.CastContext) (skills.CastPlan, error) {
		p := skills.StandardCost(c)
		b := &skills.Buff{Duration: skills.Permanent}
		for _, s := range stats {
			if c.Value(s.key) == 0 {
				return p, fmt.Errorf("skill%d: no %s", c.Skill.ID, s.key)
			}
			b.Stats = append(b.Stats, skills.StatValue{Stat: s.name, Value: c.Value(s.key)})
		}
		p.Self = b
		p.Toggle = true
		return p, nil
	}
}

// mechRiding (35001002) boards the mech: MONSTER_RIDING carries the mech
// item and the WZ enhanced stats; with Extreme Mech (35120000) learned Java
// applies 35120000's effect instead, so its stats are used.
func mechRiding(c *skills.CastContext) (skills.CastPlan, error) {
	v := c.Values
	if x := skillValues(extremeMech, learned(c.Caster, extremeMech)); x != nil {
		v = x
	}
	return mount(c, mechMount, enhancedStats(v)), nil
}

// mechMode (35111004, 35121005, 35121013) is Java's MECH_CHANGE = skill
// level for skills.Permanent. Like Java there is no toggle: the client's
// recast (0x95) applies the mode again and the mode ends from the buff icon
// (0x96). Siege Mode cast inside the Missile Tank becomes Siege Mode 2 (Java
// MapleStatEffect: 35121013 at the cast level), replacing the tank's
// MECH_CHANGE; when 35121013 ends its OnEnd puts the tank back.
func mechMode(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.Self = &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "MECH_CHANGE", Value: c.Level}}}
	if c.Skill.ID == 35111004 && buffSource(c.Caster, "MECH_CHANGE", c.Now) == missileTank {
		p.ApplyAs = siege2
	}
	return p, nil
}

// siege2End is Java MapleCharacter: when Siege Mode 2 ends (not overwritten
// by another mode, which leaves a MECH_CHANGE behind), the tank (35121005) is
// applied again at its level.
func siege2End(c *skills.EndContext) {
	lv := learned(c.Character, missileTank)
	if _, mode := skills.ActiveStat(c.Character, "MECH_CHANGE", c.Now); lv < 1 || mode {
		return
	}
	skills.ApplyBuff(c.Character.ID, missileTank, lv, skills.BuffChange{Buff: &skills.Buff{Duration: skills.Permanent, Stats: []skills.StatValue{{Stat: "MECH_CHANGE", Value: lv}}}})
}

// mechCritical is Java PlayerStats while a mech mode runs: the MECH_CHANGE
// source skill's cr is added to the critical rate.
func mechCritical(id int) func(*skills.PassiveContext) skills.Bonus {
	return func(c *skills.PassiveContext) skills.Bonus {
		src := buffSource(c.Character, "MECH_CHANGE", c.Now)
		if src != id && !(id == 35111004 && src == siege2) { // 35121013 is never learned.
			return skills.Bonus{}
		}
		return skills.Bonus{Critical: c.Values["cr"]}
	}
}

// mechanicMastery (35100000): v148 UpdatePassiveSkillData (0x91667E) applies
// its psd=1 padX and cr unconditionally whenever learned, while the gun
// mastery getter (0x6D8DDE) adds mastery (when 35120000 is not learned) and
// x accuracy when a gun is equipped.
func mechanicMastery(c *skills.PassiveContext) skills.Bonus {
	b := skills.Bonus{WATK: c.Values["padX"], Critical: c.Values["cr"]}
	if weaponType(c.Character) == gun {
		b.Mastery, b.ACC = c.Values["mastery"], c.Values["x"]
		if learned(c.Character, extremeMech) > 0 {
			b.Mastery = 0
		}
	}
	return b
}

// amplifier keeps the owner's activation icon. Actual damage amplification
// depends on the member's position in the native AF-11 affected area.
func amplifier(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	if c.Value("x") < 1 {
		return p, fmt.Errorf("skill35121010: no x")
	}
	duration, err := buffTime(c)
	if err != nil {
		return p, err
	}
	p.Self = &skills.Buff{Duration: duration, Stats: []skills.StatValue{{Stat: "DAMAGE_BUFF", Value: c.Value("x")}}}
	return p, nil
}

// healingRobot (35111011) is Java SummonHandler.SubSummon: at most once a
// second the owner regains hp percent of MaxHP.
func healingRobot(c combat.SubSummonContext) (combat.SubSummonResult, error) {
	if c.SubSkill != 35111011 || c.Now.Sub(c.LastSub) < time.Second {
		return combat.SubSummonResult{}, fmt.Errorf("skill35111011: sub rejected")
	}
	return combat.SubSummonResult{HealHP: c.Character.MaxHP * c.SubValues["hp"] / 100}, nil
}

// perfectArmor (35101007) reflects PERFECT_ARMOR percent of the damage taken
// to the attacking monster, at most a tenth of its maximum HP (Java
// MapleCharacter bounce damage). Java reflects every hit; the WZ prop (20)
// is the chance the skill description gives, so it is rolled here.
func perfectArmor(c *skills.TriggerContext) *skills.BuffChange {
	pct, ok := c.Stat("PERFECT_ARMOR")
	mob, alive := c.Attacker()
	if !ok || !alive || c.Damage <= 0 || mob.MaxHP <= 0 {
		return nil
	}
	if prop := c.Value("prop"); prop > 0 && prop < 100 && rollDie(100) > prop {
		return nil
	}
	dmg := min(int64(c.Damage)*int64(pct)/100, mob.MaxHP/10)
	if dmg < 1 {
		return nil
	}
	return &skills.BuffChange{MobHits: []combat.ServerHit{{ObjectID: mob.ObjectID, Damage: int(dmg)}}}
}

// missileUpkeep is Java doRecovery while Missile Tank (35121005) runs: every
// tick it costs u MP, and without u MP both the tank (MECH_CHANGE) and the
// mech (MONSTER_RIDING, 35001002) end. Each of the two skills ticks its own
// rows; the mech never pays.
func missileUpkeep(c *skills.TriggerContext) *skills.BuffChange {
	if len(c.Rows) == 0 || buffSource(c.Character, "MECH_CHANGE", c.Now) != missileTank {
		return nil
	}
	v := skillValues(missileTank, learned(c.Character, missileTank))
	if v == nil || v["u"] < 1 {
		return nil
	}
	if c.Character.MP < v["u"] {
		return &skills.BuffChange{Cancel: true}
	}
	if c.Skill.ID != missileTank {
		return nil
	}
	return &skills.BuffChange{CostMP: v["u"]}
}

// satellites in Java's cancel order: cancelEffectFromBuffStat(PUPPET) ends
// one satellite effect per proc.
var satellites = []int{35111001, 35111009, 35111010}

// satelliteSafety is Java MapleCharacter's Satellite Safety: a hit of at
// least SATELLITESAFE_PROC percent of MaxHP while a satellite (PUPPET) is out
// costs one satellite; the core already absorbs SATELLITESAFE_ABSORB percent.
func satelliteSafety(c *skills.TriggerContext) *skills.BuffChange {
	proc, ok := skills.ActiveStat(c.Character, "SATELLITESAFE_PROC", c.Now)
	if !ok || len(c.Rows) == 0 || c.Damage <= 0 || c.Damage*100 < c.Character.MaxHP*proc {
		return nil
	}
	for _, id := range satellites {
		if id == c.Skill.ID {
			return &skills.BuffChange{Cancel: true}
		}
		if skills.ActiveBuff(c.Character, id, c.Now) {
			return nil // An earlier satellite goes first.
		}
	}
	return nil
}

// siegeInTank is Java rangedAttack: Heavy Machine Gun fired from the Missile
// Tank (MECH_CHANGE from 35121005 or its 35121013) is Siege Mode 2.
func siegeInTank(c domain.Character, now time.Time) int {
	switch buffSource(c, "MECH_CHANGE", now) {
	case missileTank, 35121013:
		return 35121013
	}
	return 0
}

// robotFactory (35121009) is Java SummonHandler.SubSummon: at most every two
// seconds the factory makes three bots (35121011) at its level.
func robotFactory(c combat.SubSummonContext) (combat.SubSummonResult, error) {
	if c.SubSkill != 35121009 || c.Now.Sub(c.LastSub) < 2*time.Second {
		return combat.SubSummonResult{}, fmt.Errorf("skill35121009: sub rejected")
	}
	bot := combat.SummonChild{SkillID: 35121011, Level: c.SummonLvl}
	return combat.SubSummonResult{Spawn: []combat.SummonChild{bot, bot, bot}}, nil
}
