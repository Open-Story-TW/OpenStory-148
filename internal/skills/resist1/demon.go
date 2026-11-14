package resist1

import (
	"fmt"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/skills"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// Demon Slayer 3100–3112. Every attack is a close-range attack (WZ info type
// 1 / 52, no magicDamage); Grim Scythe, Soul Eater and Carrion Breath carry
// the keydown charge word, which the S0b codec already reads.

// Demon Lash (31000004) and the hidden follow-up swings the client sends for
// its third and fourth hits (GameConstants.getLinkedAttackSkill).
var demonLash = map[int]bool{31000004: true, 31001006: true, 31001007: true, 31001008: true}

type demonAttack struct {
	id      int
	onHit   []combat.MobStatusApply
	prepare []func(*combat.AttackContext) error
	drain   func(*combat.AttackContext, int64) (int, int)
	// levelFrom is the learned skill whose level authorizes a hidden
	// follow-up (Java getLinkedAttackSkill).
	levelFrom int
}

var demonAttacks = []demonAttack{
	// 惡魔殺手 3100
	{id: 31000004, prepare: steps(lashBonus)},                      // 惡魔狂斬 Demon Lash
	{id: 31001006, levelFrom: 31000004, prepare: steps(lashBonus)}, // (hidden) Demon Lash 3rd hit
	{id: 31001007, levelFrom: 31000004, prepare: steps(lashBonus)}, // (hidden) Demon Lash 4th hit
	{id: 31001008, levelFrom: 31000004, prepare: steps(lashBonus)}, // (hidden) Demon Lash 4th hit
	{id: 31001000}, // 惡魔鐮刀 Grim Scythe
	// 3110
	{id: 31101000},              // 靈魂吸收 Soul Eater
	{id: 31101001},              // 黑暗疾風步 Dark Thrust
	{id: 31101002, onHit: stun}, // 惡魔追擊 Demon Chase
	// 3111
	{id: 31111000},                                              // 黑暗審判 Dark Judgement (seed marks it passive; WZ type 1)
	{id: 31111001, onHit: stun},                                 // 死亡之握 Death Grip
	{id: 31111003, drain: bloodCrow},                            // 血腥烏鴉 Blood Crow
	{id: 31111005, onHit: []combat.MobStatusApply{poison}},      // 惡魔佈雷斯 Carrion Breath
	{id: 31121000},                                              // 惡魔末日烈焰 Infernal Concussion (seed marks it passive; WZ type 1)
	{id: 31121010, levelFrom: 31121000},                         // (hidden) follow-up of 31121000
	{id: 31121001},                                              // 惡魔衝擊 Demon Impact
	{id: 31121003, prepare: steps(demonCry)},                    // 魔力吶喊 Demon Cry
	{id: 31121006, onHit: []combat.MobStatusApply{web, poison}}, // 黑暗拘束 Binding Darkness
}

// web is Java SHADOW_WEB = 1 (Binding Darkness); Legacy lets it hold bosses.
var web = combat.MobStatusApply{Stat: combat.MobWeb, Value: 1}

func steps(f ...func(*combat.AttackContext) error) []func(*combat.AttackContext) error { return f }

func init() {
	for _, a := range demonAttacks {
		kind := skills.KindAttack
		if a.onHit != nil || a.id == 31121003 {
			kind |= skills.KindMobStatus
		}
		h := skills.Handler{ID: a.id, Kind: kind, Attack: &combat.AttackSpec{Kind: combat.Melee, OnHit: a.onHit, Prepare: prepare(append([]func(*combat.AttackContext) error{rage}, a.prepare...)...), Drain: a.drain, LevelFrom: a.levelFrom}}
		if demonLash[a.id] {
			h.Attack.AfterHit = forceOrbs
		}
		switch a.id {
		case 31101002:
			// The client sends a skill cast for the teleport before the
			// attack (MCP 2026-11-02); the attack pays the forceCon.
			h.Kind |= skills.KindSpecial
			h.Cast = teleportCast
		case 31000004:
			h.OnAttack = forceGain
		case 31121006:
			h.Kind |= skills.KindPassive
			h.Passive = func(c *skills.PassiveContext) skills.Bonus {
				return skills.Bonus{IgnoreDefense: c.Values["ignoreMobpdpR"]}
			}
		}
		skills.Register(h)
	}

	skills.Register(skills.Handler{ID: 31001001, Kind: skills.KindBuff, Cast: value("BOOSTER", "x")})      // 惡魔推進器 Booster
	skills.Register(skills.Handler{ID: 31101003, Kind: skills.KindBuff, Cast: value("PERFECT_ARMOR", "y"), // 黑暗復仇 Dark Revenge
		OnDamaged: darkRevenge, Attack: &combat.AttackSpec{MobCast: &combat.MobCastSpec{Statuses: []combat.MobStatusApply{{Stat: combat.MobStun, Value: 1, SubTime: true}}}}})
	skills.Register(skills.Handler{ID: 31111004, Kind: skills.KindBuff, Cast: blackHeartedStrength})                                // 黑暗耐力
	skills.Register(skills.Handler{ID: 31121002, Kind: skills.KindPartyBuff, Cast: party(value("COMBO_DRAIN", "x"))})               // 吸血鬼之觸 Vampiric Touch
	skills.Register(skills.Handler{ID: 31121004, Kind: skills.KindPartyBuff, Cast: party(value("MAPLE_WARRIOR", "x"))})             // 楓葉祝福 Maple Warrior
	skills.Register(skills.Handler{ID: 31121007, Kind: skills.KindBuff, Cast: boundlessRage})                                       // 無限力量 Boundless Rage
	skills.Register(skills.Handler{ID: 31121005, Kind: skills.KindBuff | skills.KindMorph | skills.KindAttack, Cast: metamorphosis, // 變形 Dark Metamorphosis
		Attack: &combat.AttackSpec{Kind: combat.Melee, Prepare: metamorphosisHit}})

	passive := func(id int, f func(*skills.PassiveContext) skills.Bonus) {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
	}
	passive(31000002, func(c *skills.PassiveContext) skills.Bonus { // 黑暗敏捷
		return skills.Bonus{ACC: c.Values["accX"], Speed: c.Values["psdSpeed"], Jump: c.Values["psdJump"]}
	})
	passive(31000003, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{MaxHPPercent: c.Values["mhpR"]} }) // HP增加
	passive(31100004, func(c *skills.PassiveContext) skills.Bonus {                                                        // 武器精通: Java reads x, WZ stores the accuracy in accX
		if w := weaponType(c.Character); w != oneHandedAxe && w != oneHandedBlunt {
			return skills.Bonus{}
		}
		return skills.Bonus{Mastery: c.Values["mastery"], ACC: c.Values["accX"]}
	})
	passive(31100005, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
	}) // 體能訓練
	passive(31100006, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{WATK: c.Values["padX"], Critical: c.Values["cr"]}
	}) // 憤怒
	passive(31110006, func(c *skills.PassiveContext) skills.Bonus { // 邪惡酷刑: Legacy applies it to every target
		return skills.Bonus{DamagePercent: c.Values["x"], BossDamagePercent: c.Values["x"], Critical: c.Values["y"]}
	})
	passive(31110007, func(c *skills.PassiveContext) skills.Bonus { // 精神集中
		return skills.Bonus{DamagePercent: c.Values["damR"], BossDamagePercent: c.Values["damR"]}
	})
	skills.Register(skills.Handler{ID: 31110008, Kind: skills.KindPassive, OnDamaged: powerGuard, // 力量防禦 Power Guard
		Passive: func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{AvoidRate: c.Values["x"]} }})
	skills.Register(skills.Handler{ID: 31110009, Kind: skills.KindPassive, OnTick: demonForceRegen, TickEvery: 4 * time.Second}) // 強化惡魔之力 (Demon Lash part in forceGain)
	passive(31120008, func(c *skills.PassiveContext) skills.Bonus {                                                              // 進階武器精通
		return skills.Bonus{WATK: c.Values["padX"], Mastery: c.Values["mastery"], CriticalDamageMin: c.Values["criticaldamageMin"]}
	})
	passive(31120009, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{WDEFPercent: c.Values["t"]} }) // 堅硬肌膚: Java percent_wdef += t
	// Demon Lash enhancements, read by lashBonus; 守護拳套防具 is PvP only.
	for _, id := range []int{31100007, 31110010, 31120011, 31000005} {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive})
	}

	combat.ModifyCeiling(demonCeiling)
	combat.ModifyDrain(comboDrain)
}

// lashTiers are the Demon Lash enhancements, strongest first, with the key
// holding their cumulative damage increase (WZ x is the "final" total of the
// 2nd and last tiers; the 1st tier only has damR).
var lashTiers = []struct {
	id  int
	key string
}{{31120011, "x"}, {31110010, "x"}, {31100007, "damR"}}

// lashBonus raises Demon Lash by the strongest learned enhancement. Java
// PlayerStats puts it in damageIncrease keyed by skill (each tier replaces
// the previous) but DamageParse looks it up by monster object id, so Legacy
// never applies it; this follows the WZ intent.
func lashBonus(ctx *combat.AttackContext) error {
	for _, t := range lashTiers {
		if lv := learnedLevel(ctx.Character, t.id, ctx.Now); lv > 0 {
			v, err := skillValues(t.id, lv)
			if err != nil {
				return err
			}
			ctx.Effect.Damage = ctx.Effect.Damage * (100 + v[t.key]) / 100
			return nil
		}
	}
	return nil
}

// forceGain is Java handleForceGain for Demon Lash hits: every damaged
// monster yields 1/2/3/4 DF at levels <30/<70/<120/≥120, and with Improving
// Demon Force (31110009) one more at its prop percent. Java's demon base
// mpRecoverProp of 100 would make that extra orb certain; the WZ chance is
// used. DF is MP for demons; forceOrbs shows the base orbs.
func forceGain(c *skills.TriggerContext) *skills.BuffChange {
	if c.Attack == nil || c.Attack.Summon || !demonLash[c.Attack.SkillID] || len(c.Attack.Targets) == 0 {
		return nil
	}
	total := forcePerHit(c.Character.Level) * len(c.Attack.Targets)
	if lv := learnedLevel(c.Character, 31110009, c.Now); lv > 0 {
		if v, err := skillValues(31110009, lv); err == nil {
			for range c.Attack.Targets {
				if rollProp(v["prop"]) {
					total++
				}
			}
		}
	}
	return &skills.BuffChange{HealMP: total}
}

// rollProp succeeds with prop percent; tests replace it.
var rollProp = func(prop int) bool { return prop >= 100 || rand.IntN(100) < prop }

// bloodCrow (31111003) heals x percent of the damage dealt, at most half of
// MaxHP (Java MapleCharacter.afterAttack).
func bloodCrow(ctx *combat.AttackContext, dealt int64) (int, int) {
	return int(min(dealt*int64(ctx.Effect.X)/100, int64(ctx.Character.MaxHP/2))), 0
}

// demonCry (31121003) is Java's monster status set with the WZ meaning of
// its values: attack and defence down x percent, accuracy down z percent and
// SHOWDOWN w (EXP and drop bonus), for subTime seconds. Java writes the
// decreases as positive x.
func demonCry(ctx *combat.AttackContext) error {
	v, err := skillValues(ctx.Effect.SkillID, ctx.Effect.Level)
	if err != nil {
		return err
	}
	down := -v["x"]
	for _, s := range []combat.MobStatusApply{
		{Stat: combat.MobPAD, Value: down}, {Stat: combat.MobPDR, Value: down},
		{Stat: combat.MobMAD, Value: down}, {Stat: combat.MobMDR, Value: down},
		{Stat: combat.MobACC, Value: -v["z"]}, {Stat: combat.MobShowdown, Value: v["w"]},
	} {
		s.SubTime = true
		ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, s)
	}
	return nil
}

// darkRevenge (31101003): while PERFECT_ARMOR is on, y percent of the damage
// taken bounces back to the attacking monster (Java caps it at a tenth of
// the monster's max HP), and the monster
// is stunned for subTime at prop percent (the registered MobCast). The
// damage taken is not reduced.
func darkRevenge(c *skills.TriggerContext) *skills.BuffChange {
	v, ok := c.Stat("PERFECT_ARMOR")
	if !ok || c.Damage <= 0 || c.Mob == 0 || v < 1 {
		return nil
	}
	bounce := c.Damage * v / 100
	if m, ok := c.Attacker(); ok && m.MaxHP > 0 {
		bounce = int(min(int64(bounce), m.MaxHP/10))
	}
	if bounce < 1 {
		return nil
	}
	return &skills.BuffChange{MobHits: []combat.ServerHit{{ObjectID: c.Mob, Damage: bounce}}, MobCast: []int32{c.Mob}}
}

// blackHeartedStrength (31111004): ABNORMAL_STATUS_R y, ELEMENTAL_STATUS_R z,
// DEFENCE_BOOST_R x (Java MapleStatEffect).
func blackHeartedStrength(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("ABNORMAL_STATUS_R", c.Value("y")), stat("ELEMENTAL_STATUS_R", c.Value("z")), stat("DEFENCE_BOOST_R", c.Value("x")))
}

// boundlessRage (31121007): BOUNDLESS_RAGE 1; skills cost no DF while it runs
// (Java calcMPChange). The cast itself costs forceCon.
func boundlessRage(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("BOUNDLESS_RAGE", 1))
}

// metamorphosis (31121005) is a self buff (Java refuses it on others):
// PIRATES_REVENGE damR and DARK_METAMORPHOSIS mobCount. The client only
// starts the auto-attacks (31121005 melee attacks) while the local bit 167
// is set (S2, 2026-11-02); Legacy had commented the local stat out while
// chasing a stuck-potion bug and only broadcast it to others as 6.
func metamorphosis(c *skills.CastContext) (skills.CastPlan, error) {
	if c.Value("damR") < 1 {
		return cost(c), fmt.Errorf("skill31121005: no damR")
	}
	return buff(c, stat("PIRATES_REVENGE", c.Value("damR")), stat("DARK_METAMORPHOSIS", max(1, c.Value("mobCount"))))
}

// demonCeiling applies the damage buffs Java PlayerStats multiplies into
// dam_r for R1 sources: Dark Metamorphosis PIRATES_REVENGE, Dark Aura and
// Body Boost (v of 32111005).
func demonCeiling(dc combat.DamageContext, ceiling int) int {
	now := time.Now()
	scale := func(percent int) {
		if percent > 0 {
			ceiling = int(min(int64(ceiling)*int64(100+percent)/100, int64(combat.MaxLineDamage())))
		}
	}
	if row, ok := liveRow(dc.Attacker, "PIRATES_REVENGE", now, 31121005); ok {
		scale(row.Value)
	}
	if row, ok := liveRow(dc.Attacker, "DARK_AURA", now, 32001003, 32110007); ok {
		scale(row.Value)
	}
	if row, ok := liveRow(dc.Attacker, "BODY_BOOST", now, 32111005); ok {
		if v, err := skillValues(32111005, row.Level); err == nil {
			scale(v["v"])
		}
	}
	return ceiling
}

// comboDrain is Java afterAttack's COMBO_DRAIN heal for Vampiric Touch and
// Blood Drain: value percent of the damage dealt, at most half of MaxHP.
func comboDrain(ctx *combat.AttackContext, dealt int64) (int, int) {
	row, ok := liveRow(ctx.Character, "COMBO_DRAIN", ctx.Now, 31121002, 32101004)
	if !ok || row.Value < 1 {
		return 0, 0
	}
	return int(min(dealt*int64(row.Value)/100, int64(ctx.Character.MaxHP/2))), 0
}

// forcePerHit is Java handleForceGain's DF per damaged monster by level.
func forcePerHit(level int) int {
	switch {
	case level >= 120:
		return 4
	case level >= 70:
		return 3
	case level >= 30:
		return 2
	}
	return 1
}

// forceCounters is Java MapleCharacter.force, the running orb counter the
// GAIN_FORCE packet carries in its colour field, per character.
var forceCounters sync.Map // character ID -> *atomic.Int32

// forceOrbs shows the Demon Lash absorption orb from every damaged monster
// to the attacker (Java CField.gainForce(oid, gain, ++force)). The extra
// Improving Demon Force orb is not drawn.
func forceOrbs(e combat.AttackEvent) {
	if e.GainForce == nil || e.Summon {
		return
	}
	v, _ := forceCounters.LoadOrStore(e.Session.CharacterID, new(atomic.Int32))
	counter := v.(*atomic.Int32)
	gain := forcePerHit(e.Session.Character.Level)
	for _, oid := range e.Targets {
		_ = e.GainForce(oid, gain, int(counter.Add(1)))
	}
}

// rage clears the DF cost of an attack while BOUNDLESS_RAGE runs (Java
// calcMPChange); the core charges forceCon as the attack's MPCon.
func rage(ctx *combat.AttackContext) error {
	if _, ok := skills.ActiveStat(ctx.Character, "BOUNDLESS_RAGE", ctx.Now); ok {
		ctx.Effect.MPCon = 0
	}
	return nil
}

// powerGuard (31110008): when the hit packet reports a Power Guard block
// (defType 1), x percent of the time it restores y percent of MaxHP and,
// for 3111/3112, z DF (Java PlayerHandler takeDamage, addDemonMp).
func powerGuard(c *skills.TriggerContext) *skills.BuffChange {
	if c.DefType != 1 || !rollProp(c.Value("x")) {
		return nil
	}
	ch := &skills.BuffChange{HealHP: c.Character.MaxHP / 100 * c.Value("y")}
	if j := c.Character.Job; j == 3111 || j == 3112 {
		ch.HealMP = c.Value("z")
	}
	if ch.HealHP == 0 && ch.HealMP == 0 {
		return nil
	}
	return ch
}

// demonForceRegen is 31110009's DF regeneration: y every 4 s for 3111/3112
// (Java PlayerStats: shouldHealMP += y, mpRecoverTime 4000; demons have no
// other MP regeneration).
func demonForceRegen(c *skills.TriggerContext) *skills.BuffChange {
	if j := c.Character.Job; j != 3111 && j != 3112 || c.Character.HP <= 0 || c.Value("y") < 1 || c.Character.MP >= c.Character.MaxMP {
		return nil
	}
	return &skills.BuffChange{HealMP: c.Value("y")}
}

// teleportCast is the free cast packet Demon Chase sends for its teleport
// before the attack; the core only shows it.
func teleportCast(*skills.CastContext) (skills.CastPlan, error) { return skills.CastPlan{}, nil }

// metamorphosisHit is the auto-attack the client sends every ~500 ms while
// DARK_METAMORPHOSIS runs (31121005 melee attacks). The cast paid the WZ
// hpCon/forceCon; the hits are free (train 38: each hit charged 300 HP and
// 75 DF and was rejected once DF ran out).
func metamorphosisHit(ctx *combat.AttackContext) error {
	ctx.Effect.MPCon, ctx.Effect.HPCon = 0, 0
	return nil
}
