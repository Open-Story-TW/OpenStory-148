package aranevan

import (
	"errors"
	"maple-go-server/internal/combat"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/skills"
	"time"
)

// polearm is the Aran weapon category (WZ weapon=44, item id / 10000).
const polearm = 144

// Aran 2100, 2110, 2111, 2112 (the beginner 2000 belongs to common).
func init() {
	attack := func(id int, s *combat.AttackSpec) {
		kind := skills.KindAttack
		if len(s.OnHit) > 0 {
			kind |= skills.KindMobStatus
		}
		skills.Register(skills.Handler{ID: id, Kind: kind, Attack: s})
	}
	passive := func(id int, f func(*skills.PassiveContext) skills.Bonus) {
		skills.Register(skills.Handler{ID: id, Kind: skills.KindPassive, Passive: f})
	}

	// Aran 2100
	skills.Register(skills.Handler{ID: 21000000, Kind: skills.KindPassive | skills.KindBuff, OnClientCombo: comboCounter, Passive: comboAbility, CounterPacket: comboPacket}) // Combo Ability
	attack(21000002, melee(nil))                                                                                                                                              // Double Swing
	attack(21000004, ranged(nil, damageFrom(21110002, "w")))                                                                                                                  // Combo Smash: +w with Full Swing
	passive(21000005, nil)                                                                                                                                                    // PvP armor (Java pvpSkills only)
	skills.Register(skills.Handler{ID: 21001001, Kind: skills.KindSpecial | skills.KindPassive, Cast: skills.CostOnly, Passive: skills.MovementPassive})                      // Combat Step
	skills.Register(skills.Handler{ID: 21001003, Kind: skills.KindBuff, Cast: polearmBooster})                                                                                // Polearm Booster

	// Aran 2110
	passive(21100000, mastery)                                                                            // Polearm Mastery
	attack(21100001, melee(nil))                                                                          // Triple Swing
	attack(21100002, melee(nil, damageFrom(21120011, "damR")))                                            // Final Charge
	skills.Register(skills.Handler{ID: 21100005, Kind: skills.KindBuff, Cast: value("COMBO_DRAIN", "x")}) // Combo Drain: heals in comboDrain
	attack(21100007, ranged(nil, damageFrom(21120002, "z")))                                              // Combo Fenrir: +z with Overswing
	passive(21100008, func(c *skills.PassiveContext) skills.Bonus {
		return skills.Bonus{STR: c.Values["strX"], DEX: c.Values["dexX"]}
	}) // Physical Training
	attack(21100010, melee(nil))                                                                                                                                               // Final Attack
	skills.Register(skills.Handler{ID: 21101003, Kind: skills.KindBuff | skills.KindAttack | skills.KindMobStatus, Cast: value("BODY_PRESSURE", "x"), Attack: bodyPressure()}) // Body Pressure
	skills.Register(skills.Handler{ID: 21101006, Kind: skills.KindBuff | skills.KindPassive | skills.KindMobStatus, Cast: value("WK_CHARGE", "x"), Passive: snowChargeDamage}) // Snow Charge

	// Aran 2111
	passive(21110000, comboCritical)                                // Combo Critical
	passive(21110002, nil)                                          // Full Swing: dummy of 21110007/8, +w on Combo Smash
	attack(21110003, melee(nil, damageFrom(21120011, "damR")))      // Final Toss
	attack(21110006, melee(stun))                                   // Rolling Spin: STUN
	attack(21110007, linked(21110002))                              // (hidden) Full Swing - Double
	attack(21110008, linked(21110002))                              // (hidden) Full Swing - Triple
	passive(21110010, func(c *skills.PassiveContext) skills.Bonus { // 攀爬攻擊: Java reads it as 21111010
		return skills.Bonus{IgnoreDefense: c.Values["ignoreMobpdpR"], BossDamagePercent: c.Values["bdR"]}
	})
	attack(21110011, melee(nil, poison))                                                                      // Combo Judgment: POISON DoT; v145 sends it as 0x4B (Java lists it as ranged)
	skills.Register(skills.Handler{ID: 21111001, Kind: skills.KindBuff, Cast: smartKnockback})                // Smart Knockback
	skills.Register(skills.Handler{ID: 21111009, Kind: skills.KindSpecial, Cast: comboRecharge})              // Combo Recharge
	skills.Register(skills.Handler{ID: 21111012, Kind: skills.KindPartyBuff, Cast: skills.StandardPartyBuff}) // Maha's Blessing: pad, mad

	// Aran 2112
	passive(21120001, highMastery)                                                                                                                // High Mastery
	passive(21120002, nil)                                                                                                                        // Overswing: dummy of 21120009/10, +z on Combo Fenrir
	passive(21120004, func(c *skills.PassiveContext) skills.Bonus { return skills.Bonus{MaxHPPercent: c.Values["mhpR"]} })                        // High Defense (WZ; x needs core)
	attack(21120005, melee(nil))                                                                                                                  // Final Blow: WZ cr 100
	attack(21120006, tempest())                                                                                                                   // Combo Tempest
	skills.Register(skills.Handler{ID: 21120007, Kind: skills.KindPartyBuff, Cast: party(value("COMBO_BARRIER", "x"))})                           // Combo Barrier: pdd, mdd
	attack(21120009, linked(21120002))                                                                                                            // (hidden) Overswing - Double
	attack(21120010, linked(21120002))                                                                                                            // (hidden) Overswing - Triple
	passive(21120011, nil)                                                                                                                        // Fast Move: +damR on Final Charge / Final Toss
	skills.Register(skills.Handler{ID: 21120012, Kind: skills.KindAttack | skills.KindPassive, Attack: melee(nil), Passive: advancedFinalAttack}) // Advanced Final Attack
	skills.Register(skills.Handler{ID: 21121000, Kind: skills.KindPartyBuff, Cast: party(value("MAPLE_WARRIOR", "x"))})                           // Maple Warrior
	skills.Register(skills.Handler{ID: 21121003, Kind: skills.KindBuff, Cast: value("STANCE", "prop")})                                           // Freezing Posture
	skills.Register(skills.Handler{ID: 21121008, Kind: skills.KindSpecial, Cast: skills.HerosWill})                                               // Hero's Will

	combat.ModifyDrain(comboDrain)
}

// aranHits are the attacks Java DamageParse gives the Snow Charge slow and
// the Body Pressure neutralise while those buffs run.
var aranHits = map[int]bool{
	21000002: true, 21100001: true, 21100002: true, 21110003: true, 21110006: true, 21110007: true, 21110008: true,
	21120005: true, 21120006: true, 21120009: true, 21120010: true,
}

// melee is an Aran close-range attack (WZ info type 52/53).
func melee(onHit []combat.MobStatusApply, prepare ...func(*combat.AttackContext) error) *combat.AttackSpec {
	return &combat.AttackSpec{Kind: combat.Melee, OnHit: onHit, Prepare: steps(append(prepare, aranHit)...)}
}

// ranged is an Aran attack Java PlayerHandler.rangedAttack parses (Combo Smash,
// Combo Fenrir, Combo Tempest; v145 sends Combo Judgment as 0x4B): the ranged packet without a
// projectile.
func ranged(onHit []combat.MobStatusApply, prepare ...func(*combat.AttackContext) error) *combat.AttackSpec {
	return &combat.AttackSpec{Kind: combat.Shoot, Ammo: combat.AmmoNone, OnHit: onHit, Prepare: steps(append(prepare, aranHit)...)}
}

// linked is a hidden Full Swing / Overswing attack: the client sends it for
// the learned dummy skill (Java getLinkedAranSkill), whose level authorizes
// it; the damage is the hidden skill's own WZ.
func linked(parent int) *combat.AttackSpec {
	s := melee(nil)
	s.LevelFrom = parent
	return s
}

// aranHit adds Java DamageParse's buff statuses to the listed attacks: Snow
// Charge (WK_CHARGE of 21101006) slows by its value for the skill's y
// seconds; Body Pressure (21101003) neutralises at its prop for x seconds
// (CanInflict keeps bosses out of the neutralise).
func aranHit(ctx *combat.AttackContext) error {
	if !aranHits[ctx.Effect.SkillID] {
		return nil
	}
	if row, ok := activeRow(ctx.Character, 21101006, "WK_CHARGE", ctx.Now); ok {
		if v, err := values(21101006, row.Level); err == nil && v["y"] > 0 {
			ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobSpeed, Value: row.Value, Prop: 100, Duration: time.Duration(v["y"]) * time.Second})
		}
	}
	if row, ok := activeRow(ctx.Character, 21101003, "BODY_PRESSURE", ctx.Now); ok && row.Value > 0 {
		if v, err := values(21101003, row.Level); err == nil {
			ctx.Effect.MonsterStatus = append(ctx.Effect.MonsterStatus, combat.MobStatusApply{Stat: combat.MobBodyPressure, Value: 1, Prop: max(1, v["prop"]), Duration: time.Duration(row.Value) * time.Second})
		}
	}
	return nil
}

// damageFrom is Java PlayerStats.damageIncrease: another learned skill's WZ
// key is added to the attack's damage percent.
func damageFrom(id int, key string) func(*combat.AttackContext) error {
	return func(ctx *combat.AttackContext) error {
		ctx.Effect.Damage += learnedValue(ctx.Character, id, key, ctx.Now)
		return nil
	}
}

// bodyPressure is the body (touch) attack of 21101003; Java DamageParse only
// accepts a body attack while BODY_PRESSURE is on.
func bodyPressure() *combat.AttackSpec {
	return &combat.AttackSpec{Kind: combat.Body, Prepare: func(ctx *combat.AttackContext) error {
		if _, ok := activeRow(ctx.Character, 21101003, "BODY_PRESSURE", ctx.Now); !ok {
			return errors.New("body pressure is not active")
		}
		return nil
	}}
}

// tempest (21120006) freezes for twice the WZ time; Java lets it deal up to
// the monster's HP (500000 on bosses) instead of the normal ceiling.
func tempest() *combat.AttackSpec {
	s := ranged(nil, freeze)
	s.MaxDamage = func(dc combat.DamageContext) int {
		if dc.Mob.Boss {
			return 500000
		}
		return int(min(max(dc.HP, 1), int64(combat.MaxLineDamage())))
	}
	return s
}

// polearmBooster (21001003) is Java BOOSTER = -y.
func polearmBooster(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, stat("BOOSTER", -c.Value("y")))
}

// smartKnockback (21111001) is SMART_KNOCKBACK x plus Java's generic
// epad/epdd/emdd enhanced stats.
func smartKnockback(c *skills.CastContext) (skills.CastPlan, error) {
	return buff(c, append([]skills.StatValue{stat("SMART_KNOCKBACK", c.Value("x"))}, enhancedStats(c)...)...)
}

// mastery (21100000) is Java calculateMasteryValue for a polearm: mastery and
// x accuracy.
func mastery(c *skills.PassiveContext) skills.Bonus {
	if weaponType(c.Character) != polearm {
		return skills.Bonus{}
	}
	// v148 0x6D922A..0x6D9280: High Mastery (21120001) replaces this mastery.
	return skills.Bonus{Mastery: c.BasicMastery(21120001), ACC: c.Values["x"]}
}

// snowChargeDamage is Java PlayerStats for 2110-2112: damR of 21101006
// whether or not the charge runs.
func snowChargeDamage(c *skills.PassiveContext) skills.Bonus {
	if j := c.Character.Job; j < 2110 || j > 2112 {
		return skills.Bonus{}
	}
	return skills.Bonus{DamagePercent: c.Values["damR"]}
}

// comboCritical (21110000) is a v148 psd passive: its WZ common row gives a
// flat cr and criticaldamageMax for 2111 and 2112. x and y only describe the
// combo counter text and are not part of the passive rate.
func comboCritical(c *skills.PassiveContext) skills.Bonus {
	if j := c.Character.Job; j != 2111 && j != 2112 {
		return skills.Bonus{}
	}
	return skills.Bonus{Critical: c.Values["cr"], CriticalDamage: c.Values["criticaldamageMax"]}
}

// highMastery (21120001) is Java PlayerStats case 2112: x attack, mastery and
// criticaldamageMin.
func highMastery(c *skills.PassiveContext) skills.Bonus {
	if c.Character.Job != 2112 {
		return skills.Bonus{}
	}
	return skills.Bonus{WATK: c.Values["x"], Mastery: c.Values["mastery"], CriticalDamageMin: c.Values["criticaldamageMin"]}
}

// advancedFinalAttack (21120012) adds padX attack and ar accuracy percent.
func advancedFinalAttack(c *skills.PassiveContext) skills.Bonus {
	return skills.Bonus{WATK: c.Values["padX"], ACCPercent: c.Values["ar"]}
}

// Combo counter (Java PlayerHandler.AranCombo).
const (
	comboMax  = 30000
	comboIdle = 7 * time.Second
)

// comboCounter keeps the Aran combo on 21000000's ARAN_COMBO row: Counter is
// the combo, the row is renewed for skills.Permanent on every change so its
// expiry minus Permanent is the last combo hit. Like Java
// PlayerHandler.AranCombo it runs for each RecvAranCombo (0xEA) the client
// sends after a hit, adds one up to 30000 and starts over when the last hit
// is older than 7 seconds; the core answers with comboPacket (0x153). The
// value is the combo Java last applied as a buff: every 10 combos up to 10
// times the level (Java applyComboBuff only at 10..100 when the level allows
// it).
func comboCounter(c *skills.TriggerContext) *skills.BuffChange {
	if !domain.IsAranJob(c.Character.Job) {
		return nil
	}
	return comboChange(lastCombo(c.Rows, c.Now), 1, c.Level)
}

// lastCombo is the live combo of 21000000's rows at now (0 after 7 idle
// seconds).
func lastCombo(rows []domain.SkillBuff, now time.Time) int {
	for _, r := range rows {
		if r.Stat == "ARAN_COMBO" && r.ExpiresAt.After(now) {
			if now.Sub(r.ExpiresAt.Add(-skills.Permanent)) > comboIdle {
				return 0
			}
			return r.Counter
		}
	}
	return 0
}

// comboChange renews the ARAN_COMBO row with combo + add.
func comboChange(combo, add, level int) *skills.BuffChange {
	combo = min(comboMax, combo+add)
	return &skills.BuffChange{Buff: &skills.Buff{Duration: skills.Permanent, Counter: combo, Stats: []skills.StatValue{stat("ARAN_COMBO", comboBuffValue(combo, level))}}}
}

// comboBuffValue is the ARAN_COMBO value Java holds for a combo count.
func comboBuffValue(combo, level int) int { return 10 * min(combo/10, level, 10) }

// comboPacket is the native combo count (Java CField.testCombo, 0x153), the
// answer to the client's 0xEA. Pushing it right after an attack settled,
// before the client's own 0xEA, disconnected Final Charge (21100002).
func comboPacket(counter int) []byte {
	w := protocol.NewWriter(0x16a)
	w.I32(int32(counter))
	return w.Packet()
}

// comboAbility is Java PlayerStats: attack += ARAN_COMBO / 10.
func comboAbility(c *skills.PassiveContext) skills.Bonus {
	row, ok := activeRow(c.Character, 21000000, "ARAN_COMBO", c.Now)
	if !ok {
		return skills.Bonus{}
	}
	return skills.Bonus{WATK: row.Value / 10}
}

// comboRecharge (21111009) pays mpCon and x% of the maximum HP (Java hpR =
// -x/100 on getCurrentMaxHp), starts the cooldown and adds y combos to the
// caster's ARAN_COMBO row (Java isComboRecharge: setLastCombo now, then the
// combo buff). Legacy reads the amount from a server setting; WZ y is used.
func comboRecharge(c *skills.CastContext) (skills.CastPlan, error) {
	p := skills.StandardCost(c)
	p.HPCost += c.Caster.MaxHP * c.Value("x") / 100
	level := learned(c.Caster, 21000000, c.Now)
	if p.Cooldown <= 0 || level < 1 || c.Value("y") < 1 {
		return p, errors.New("combo recharge needs combo ability and a cooldown")
	}
	var rows []domain.SkillBuff
	for _, b := range c.Caster.SkillBuffs {
		if b.ID == 21000000 {
			rows = append(rows, b)
		}
	}
	p.Changes = []skills.SkillChange{{SkillID: 21000000, Change: *comboChange(lastCombo(rows, c.Now), c.Value("y"), level)}}
	return p, nil
}

// comboDrain is Java MapleCharacter.afterAttack for COMBO_DRAIN of 21100005:
// every attack heals x% of the damage dealt, at most half of MaxHP.
func comboDrain(ctx *combat.AttackContext, dealt int64) (int, int) {
	row, ok := activeRow(ctx.Character, 21100005, "COMBO_DRAIN", ctx.Now)
	if !ok || dealt < 1 || row.Value < 1 {
		return 0, 0
	}
	return int(min(dealt*int64(row.Value)/100, int64(ctx.Character.MaxHP/2))), 0
}
